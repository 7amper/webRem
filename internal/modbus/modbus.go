package modbus

import (
	"fmt"
	"math"
	"sync"
	"time"

	"rockpi-scan/internal/config"
	"rockpi-scan/internal/tcp"
)

// Режимы работы привода (OperationMode)
const (
	ModePosition = 0
	ModeSpeed    = 1
	ModeCurrent  = 2

	DirCW = 0x08 // bit4(8): 1 — CW
)

type Client struct {
	tr *tcp.Client
}

func New(addr string, timeout time.Duration) *Client {
	return &Client{tr: tcp.New(addr, timeout)}
}

func (c *Client) SetAddr(addr string) { c.tr.SetAddr(addr) }
func (c *Client) Close()              { c.tr.Close() }
func (c *Client) Connected() bool     { return c.tr.Connected() }

// ReadRegs — FC 0x03
func (c *Client) ReadRegs(unit byte, addr, qty uint16) ([]uint16, error) {
	if qty == 0 {
		return nil, nil
	}
	pdu := []byte{0x03, byte(addr >> 8), byte(addr), byte(qty >> 8), byte(qty)}
	resp, err := c.tr.Request(unit, pdu)
	if err != nil {
		return nil, err
	}
	if len(resp) < 2 || resp[0] != 0x03 {
		return nil, fmt.Errorf("modbus: неверный ответ на чтение")
	}
	n := int(resp[1])
	if n != int(qty)*2 || len(resp) < 2+n {
		return nil, fmt.Errorf("modbus: неверная длина данных")
	}
	out := make([]uint16, qty)
	for i := range out {
		out[i] = uint16(resp[2+2*i])<<8 | uint16(resp[3+2*i])
	}
	return out, nil
}

// WriteReg — FC 0x06
func (c *Client) WriteReg(unit byte, addr, val uint16) error {
	pdu := []byte{0x06, byte(addr >> 8), byte(addr), byte(val >> 8), byte(val)}
	_, err := c.tr.Request(unit, pdu)
	return err
}

// WriteRegs — FC 0x10
func (c *Client) WriteRegs(unit byte, addr uint16, vals []uint16) error {
	if len(vals) == 0 {
		return nil
	}
	pdu := make([]byte, 6+len(vals)*2)
	pdu[0] = 0x10
	pdu[1] = byte(addr >> 8)
	pdu[2] = byte(addr)
	pdu[3] = byte(len(vals) >> 8)
	pdu[4] = byte(len(vals))
	pdu[5] = byte(len(vals) * 2)
	for i, v := range vals {
		pdu[6+2*i] = byte(v >> 8)
		pdu[7+2*i] = byte(v)
	}
	_, err := c.tr.Request(unit, pdu)
	return err
}

// ---------------------------------------------------------------------------
// Привод
// ---------------------------------------------------------------------------

type DriveState struct {
	Name     string  `json:"name"`
	UnitID   byte    `json:"unitId"`
	Online   bool    `json:"online"`
	Enabled  bool    `json:"enabled"`
	Mode     string  `json:"mode"`
	Position float64 `json:"position"` // мм
	Speed    float64 `json:"speed"`    // мм/с
	Current  float64 `json:"current"`
	Fault    uint16  `json:"fault"`
	Status   uint16  `json:"status"`
	Inputs   uint16  `json:"inputs"`
	Outputs  uint16  `json:"outputs"`
	Target   float64 `json:"target"`
	Offset   float64 `json:"offset"`
	Err      string  `json:"err"`
}

type Drive struct {
	mb *Client

	mu sync.RWMutex
	c  config.Drive
	r  config.Modbus
	st DriveState
}

func NewDrive(c config.Drive, r config.Modbus, mb *Client) *Drive {
	d := &Drive{mb: mb, c: c, r: r}
	d.st = DriveState{
		Name:   c.Name,
		UnitID: c.UnitID,
		Mode:   "idle",
		Offset: c.OffsetMM,
	}
	return d
}

func (d *Drive) Configure(c config.Drive, r config.Modbus) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.c = c
	d.r = r
	d.st.Name = c.Name
	d.st.UnitID = c.UnitID
}

func (d *Drive) State() DriveState {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.st
}

func (d *Drive) Name() string { return d.c.Name }

// --- преобразование координат -------------------------------------------------

func (d *Drive) a(reg uint16) uint16 { return reg + uint16(d.r.AddrOffset) }

func (d *Drive) pulsesToMM(p int32) float64 {
	v := float64(p) / d.c.Gear
	if d.c.Invert {
		v = -v
	}
	return v + d.st.Offset
}

func (d *Drive) mmToPulses(mm float64) int32 {
	v := mm - d.st.Offset
	if d.c.Invert {
		v = -v
	}
	return int32(math.Round(v * d.c.Gear))
}

func (d *Drive) split32(v int32) []uint16 {
	hi := uint16(uint32(v) >> 16)
	lo := uint16(uint32(v) & 0xffff)
	if d.r.WordOrder == "little" {
		return []uint16{lo, hi}
	}
	return []uint16{hi, lo}
}

func (d *Drive) join32(x, y uint16) int32 {
	if d.r.WordOrder == "little" {
		x, y = y, x
	}
	return int32(uint32(x)<<16 | uint32(y))
}

func (d *Drive) write32(reg uint16, v int32) error {
	return d.mb.WriteRegs(d.c.UnitID, d.a(reg), d.split32(v))
}

// --- опрос --------------------------------------------------------------------

func (d *Drive) Poll() error {
	regs := []uint16{
		d.r.RegInput, d.r.RegOutputRead, d.r.RegRealtimeFault1,
		d.r.RegRealtimeStatus1, d.r.RegCurrentGet,
		d.r.RegSpeedGet, d.r.RegSpeedGet + 1,
		d.r.RegPositionGet, d.r.RegPositionGet + 1,
	}
	lo, hi := regs[0], regs[0]
	for _, r := range regs {
		if r < lo {
			lo = r
		}
		if r > hi {
			hi = r
		}
	}
	qty := hi - lo + 1

	vals, err := d.mb.ReadRegs(d.c.UnitID, d.a(lo), qty)
	if err != nil {
		d.mu.Lock()
		d.st.Online = false
		d.st.Err = err.Error()
		d.mu.Unlock()
		return err
	}

	get := func(r uint16) uint16 {
		idx := int(r - lo)
		if idx < 0 || idx >= len(vals) {
			return 0
		}
		return vals[idx]
	}

	spd := d.join32(get(d.r.RegSpeedGet), get(d.r.RegSpeedGet+1))
	pos := d.join32(get(d.r.RegPositionGet), get(d.r.RegPositionGet+1))

	sc := d.c.SpeedCoef
	if sc == 0 {
		sc = 1
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	d.st.Online = true
	d.st.Err = ""
	d.st.Inputs = get(d.r.RegInput)
	d.st.Outputs = get(d.r.RegOutputRead)
	d.st.Fault = get(d.r.RegRealtimeFault1)
	d.st.Status = get(d.r.RegRealtimeStatus1)
	d.st.Current = float64(int16(get(d.r.RegCurrentGet)))
	d.st.Speed = float64(spd) / sc
	d.st.Position = d.pulsesToMM(pos)
	return nil
}

// --- команды ------------------------------------------------------------------

// Enable: 1-Off, 10-on
func (d *Drive) Enable(on bool) error {
	v := uint16(1)
	if on {
		v = 10
	}
	if err := d.mb.WriteReg(d.c.UnitID, d.a(d.r.RegEnable), v); err != nil {
		return err
	}
	d.mu.Lock()
	d.st.Enabled = on
	d.mu.Unlock()
	return nil
}

func (d *Drive) writeProfile(speedMMs float64) error {
	sc := d.c.SpeedCoef
	if sc == 0 {
		sc = 1
	}
	v := int32(math.Round(math.Abs(speedMMs) * sc))
	if err := d.write32(d.r.RegSpeedSet, v); err != nil {
		return err
	}
	if err := d.write32(d.r.RegAccelerationSet, int32(d.c.Accel)); err != nil {
		return err
	}
	return d.write32(d.r.RegDecelerationSet, int32(d.c.Decel))
}

// SetSpeedMode — движение со скоростью (мм/с)
func (d *Drive) SetSpeedMode(mmps float64) error {
	if err := d.writeProfile(mmps); err != nil {
		return err
	}
	mode := uint16(ModeSpeed)
	if mmps < 0 {
		mode |= DirCW
	}
	if err := d.mb.WriteReg(d.c.UnitID, d.a(d.r.RegOperationMode), mode); err != nil {
		return err
	}
	d.mu.Lock()
	d.st.Mode = "speed"
	d.st.Target = math.NaN()
	d.mu.Unlock()
	return nil
}

// SetProfileSpeed — скорость для последующего позиционного хода
func (d *Drive) SetProfileSpeed(mmps float64) error {
	return d.writeProfile(mmps)
}

// MoveTo — позиционный ход (мм)
func (d *Drive) MoveTo(mm float64) error {
	p := d.mmToPulses(mm)
	if err := d.write32(d.r.RegPositionSet, p); err != nil {
		return err
	}
	mode := uint16(ModePosition)
	d.mu.RLock()
	delta := mm - d.st.Position
	d.mu.RUnlock()
	if delta < 0 {
		mode |= DirCW
	}
	if err := d.mb.WriteReg(d.c.UnitID, d.a(d.r.RegOperationMode), mode); err != nil {
		return err
	}
	d.mu.Lock()
	d.st.Mode = "position"
	d.st.Target = mm
	d.mu.Unlock()
	return nil
}

// Stop — останов (нулевая скорость в скоростном режиме)
func (d *Drive) Stop() error {
	if err := d.writeProfile(0); err != nil {
		return err
	}
	if err := d.mb.WriteReg(d.c.UnitID, d.a(d.r.RegOperationMode), uint16(ModeSpeed)); err != nil {
		return err
	}
	d.mu.Lock()
	d.st.Mode = "stop"
	d.st.Target = math.NaN()
	d.mu.Unlock()
	return nil
}

// SetOutputs — дискретные выходы
func (d *Drive) SetOutputs(mask uint16) error {
	if err := d.mb.WriteReg(d.c.UnitID, d.a(d.r.RegOutput), mask); err != nil {
		return err
	}
	d.mu.Lock()
	d.st.Outputs = mask
	d.mu.Unlock()
	return nil
}

// Home — привязка: текущее положение принимается за posMM.
// Вычисляется статическая поправка положения (offset).
func (d *Drive) Home(posMM float64) error {
	regs, err := d.mb.ReadRegs(d.c.UnitID, d.a(d.r.RegPositionGet), 2)
	if err != nil {
		return err
	}
	pulses := d.join32(regs[0], regs[1])

	raw := float64(pulses) / d.c.Gear
	if d.c.Invert {
		raw = -raw
	}

	d.mu.Lock()
	d.st.Offset = posMM - raw
	d.st.Position = posMM
	d.st.Target = posMM
	off := d.st.Offset
	d.mu.Unlock()

	// статическая поправка сохраняется в конфигурацию (см. control.Home)
	_ = off
	return nil
}

func (d *Drive) Offset() float64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.st.Offset
}

func (d *Drive) Config() config.Drive {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.c
}