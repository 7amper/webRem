package control

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"net"

	//"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"rockpi-scan/internal/config"
	"rockpi-scan/internal/modbus"
	"rockpi-scan/internal/scan"
	"rockpi-scan/internal/udp"
)

type ScanInfo struct {
	Running bool `json:"running"`
	Paused  bool `json:"paused"`
	Index   int  `json:"index"`
	Total   int  `json:"total"`
}

type State struct {
	Connected bool                `json:"connected"`
	ServerUp  bool                `json:"serverUp"`
	ClientUp  bool                `json:"clientUp"`
	LinkErr   string              `json:"linkErr"`
	UDPOn     bool                `json:"udpOn"`
	UDPSent   uint64              `json:"udpSent"`
	UDPLast   int64               `json:"udpLast"`
	Drives    []modbus.DriveState `json:"drives"`
	PosX      float64             `json:"posX"`
	PosY      float64             `json:"posY"`
	Scan      ScanInfo            `json:"scan"`
	Trail     []scan.Point        `json:"trail"`
}

type Controller struct {
	cfg *config.Manager
	mb  *modbus.Client
	ax  map[string]*modbus.Drive

	mu        sync.RWMutex
	order     []string
	connected bool
	serverUp  bool
	clientUp  bool
	linkErr   string

	udpOn    bool
	sender   *udp.Sender
	lastX    float64
	lastY    float64
	haveLast bool

	scanRunning bool
	scanPaused  bool
	scanPts     []scan.Point
	scanIdx     int
	trail       []scan.Point
	scanCancel  chan struct{}
}

func New(cfg *config.Manager) *Controller {
	cc := cfg.Get()
	addr := net.JoinHostPort(cc.Modbus.Host, strconv.Itoa(cc.Modbus.Port))
	timeout := time.Duration(cc.Modbus.TimeoutMs) * time.Millisecond

	c := &Controller{
		cfg: cfg,
		mb:  modbus.New(addr, timeout),
		ax:  map[string]*modbus.Drive{},
	}
	for _, d := range cc.Drives {
		c.ax[d.Name] = modbus.NewDrive(d, cc.Modbus, c.mb)
		c.order = append(c.order, d.Name)
	}
	if len(c.order) == 0 {
		c.order = []string{"X", "Y"}
	}
	return c
}

func (c *Controller) Close() {
	c.stopScan()
	c.mb.Close()
	c.mu.Lock()
	if c.sender != nil {
		_ = c.sender.Close()
		c.sender = nil
	}
	c.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Основной цикл
// ---------------------------------------------------------------------------

func (c *Controller) Run(ctx context.Context) {
	next := time.Now()
	lastPing := time.Now()

	for {
		cc := c.cfg.Get()
		hz := cc.Link.PollHz
		if hz < 1 {
			hz = 1
		}
		if hz > 200 {
			hz = 200
		}
		interval := time.Second / time.Duration(hz)

		next = next.Add(interval)
		if d := time.Until(next); d > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(d):
			}
		} else {
			next = time.Now()
		}

		c.applyConfig()
		c.poll()

		if time.Since(lastPing) > 2*time.Second {
			lastPing = time.Now()
			c.checkLinks()
		}
	}
}

func (c *Controller) applyConfig() {
	cc := c.cfg.Get()
	addr := net.JoinHostPort(cc.Modbus.Host, strconv.Itoa(cc.Modbus.Port))
	c.mb.SetAddr(addr)
	for _, d := range c.ax {
		d.Configure(cc.Drives[indexOf(cc.Drives, d.Name())], cc.Modbus)
	}
}

func indexOf(ds []config.Drive, name string) int {
	for i := range ds {
		if ds[i].Name == name {
			return i
		}
	}
	return 0
}

func (c *Controller) poll() {
	c.mu.RLock()
	conn := c.connected
	c.mu.RUnlock()

	if conn {
		allOffline := true
		for _, name := range c.order {
			d := c.ax[name]
			if d == nil {
				continue
			}
			if err := d.Poll(); err == nil {
				allOffline = false
			}
		}
		if allOffline {
			c.mu.Lock()
			c.connected = false
			c.linkErr = "потеря связи с приводами"
			c.mu.Unlock()
		}
	}

	c.sendUDP()
}

// ---------------------------------------------------------------------------
// Проверка доступности (ping)
// ---------------------------------------------------------------------------

//func pingHost(host string, timeout time.Duration) bool {
//	if host == "" {
//		return false
//	}
//	if _, err := exec.LookPath("ping"); err != nil {
//		// ping недоступен — считаем узел доступным (нет способа проверить)
//		return true
//	}
//	ctx, cancel := context.WithTimeout(context.Background(), timeout)
//	defer cancel()
//	cmd := exec.CommandContext(ctx, "ping", "-c", "1", "-W", "1", host)
//	return cmd.Run() == nil
//}

func (c *Controller) checkLinks() {
	cc := c.cfg.Get()
	srvHost := cc.Modbus.Host
	udpHost := cc.Link.UDPAddr
	if h, _, err := net.SplitHostPort(cc.Link.UDPAddr); err == nil {
		udpHost = h
	}

	srv := pingHost(srvHost, 1500*time.Millisecond)
	cli := pingHost(udpHost, 1500*time.Millisecond)

	c.mu.Lock()
	c.serverUp = srv
	c.clientUp = cli
	lost := c.connected && !srv
	if lost {
		c.connected = false
		c.linkErr = "потеря связи с сервером"
	}
	c.mu.Unlock()

	if lost {
		go c.disableAll()
	}
}

func (c *Controller) disableAll() {
	for _, name := range c.order {
		if d := c.ax[name]; d != nil {
			_ = d.Stop()
			_ = d.Enable(false)
		}
	}
}

// ---------------------------------------------------------------------------
// Подключение / отключение
// ---------------------------------------------------------------------------

func (c *Controller) SetConnected(on bool) error {
	cc := c.cfg.Get()
	if on {
		addr := net.JoinHostPort(cc.Modbus.Host, strconv.Itoa(cc.Modbus.Port))
		c.mb.SetAddr(addr)
		// проверяем, что приводы отвечают
		ok := false
		for _, name := range c.order {
			if d := c.ax[name]; d != nil {
				if err := d.Poll(); err == nil {
					ok = true
				}
			}
		}
		if !ok {
			return errors.New("приводы не отвечают по Modbus TCP")
		}
		c.mu.Lock()
		c.connected = true
		c.linkErr = ""
		c.mu.Unlock()

		for _, name := range c.order {
			if d := c.ax[name]; d != nil {
				_ = d.Enable(true)
			}
		}
		return nil
	}

	c.mu.Lock()
	c.connected = false
	c.mu.Unlock()
	c.stopScan()
	c.disableAll()
	return nil
}

func (c *Controller) Connected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// ---------------------------------------------------------------------------
// Ручное управление
// ---------------------------------------------------------------------------

func (c *Controller) Drive(name string) *modbus.Drive {
	return c.ax[strings.ToUpper(name)]
}

func (c *Controller) AxisNames() []string {
	out := make([]string, len(c.order))
	copy(out, c.order)
	return out
}

func (c *Controller) Enable(axis string, on bool) error {
	d := c.Drive(axis)
	if d == nil {
		return errors.New("неизвестная ось " + axis)
	}
	return d.Enable(on)
}

func (c *Controller) Speed(axis string, mmps float64) error {
	d := c.Drive(axis)
	if d == nil {
		return errors.New("неизвестная ось " + axis)
	}
	cfg := d.Config()
	max := cfg.MaxSpeed
	if mmps > max {
		mmps = max
	}
	if mmps < -max {
		mmps = -max
	}
	return d.SetSpeedMode(mmps)
}

func (c *Controller) MoveTo(axis string, mm float64) error {
	d := c.Drive(axis)
	if d == nil {
		return errors.New("неизвестная ось " + axis)
	}
	return d.MoveTo(mm)
}

func (c *Controller) Stop(axis string) error {
	if axis == "" || strings.EqualFold(axis, "all") {
		var err error
		for _, name := range c.order {
			if d := c.ax[name]; d != nil {
				if e := d.Stop(); e != nil && err == nil {
					err = e
				}
			}
		}
		return err
	}
	d := c.Drive(axis)
	if d == nil {
		return errors.New("неизвестная ось " + axis)
	}
	return d.Stop()
}

func (c *Controller) SetOutputs(axis string, mask uint16) error {
	d := c.Drive(axis)
	if d == nil {
		return errors.New("неизвестная ось " + axis)
	}
	return d.SetOutputs(mask)
}

// Home — привязка оси к указанному положению.
func (c *Controller) Home(axis string, posMM float64) error {
	d := c.Drive(axis)
	if d == nil {
		return errors.New("неизвестная ось " + axis)
	}
	if err := d.Home(posMM); err != nil {
		return err
	}
	// сохраняем статическую поправку в конфигурацию
	cc := c.cfg.Get()
	for i := range cc.Drives {
		if strings.EqualFold(cc.Drives[i].Name, axis) {
			cc.Drives[i].OffsetMM = d.Offset()
		}
	}
	return c.cfg.Set(cc)
}

// ---------------------------------------------------------------------------
// UDP
// ---------------------------------------------------------------------------

func (c *Controller) SetUDP(on bool) {
	c.mu.Lock()
	c.udpOn = on
	if !on {
		c.haveLast = false
	}
	c.mu.Unlock()
}

func (c *Controller) UDPOn() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.udpOn
}

func (c *Controller) ensureSender() (*udp.Sender, error) {
	cc := c.cfg.Get()
	var order binary.ByteOrder = binary.LittleEndian
	if strings.EqualFold(cc.Link.UDPEndian, "big") {
		order = binary.BigEndian
	}
	if c.sender != nil && c.sender.Addr() == cc.Link.UDPAddr {
		return c.sender, nil
	}
	if c.sender != nil {
		_ = c.sender.Close()
		c.sender = nil
	}
	s, err := udp.New(cc.Link.UDPAddr, order)
	if err != nil {
		return nil, err
	}
	c.sender = s
	return s, nil
}

func (c *Controller) sendUDP() {
	c.mu.Lock()
	on := c.udpOn
	c.mu.Unlock()
	if !on {
		return
	}

	x, y := c.position()
	cc := c.cfg.Get()
	step := cc.Link.UDPIntervalMM
	if step <= 0 {
		step = 0.5
	}

	c.mu.Lock()
	if c.haveLast {
		if math.Abs(x-c.lastX) < step && math.Abs(y-c.lastY) < step {
			c.mu.Unlock()
			return
		}
	}
	s, err := c.ensureSender()
	if err != nil {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	if err := s.Send(x, y); err != nil {
		return
	}
	c.mu.Lock()
	c.lastX, c.lastY = x, y
	c.haveLast = true
	c.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Сканирование
// ---------------------------------------------------------------------------

func (c *Controller) position() (float64, float64) {
	var x, y float64
	if d := c.ax["X"]; d != nil {
		x = d.State().Position
	}
	if d := c.ax["Y"]; d != nil {
		y = d.State().Position
	}
	return x, y
}

func (c *Controller) Route() []scan.Point {
	cc := c.cfg.Get()
	return scan.Build(cc.Scan)
}

func (c *Controller) StartScan() error {
	c.mu.Lock()
	if c.scanRunning {
		c.mu.Unlock()
		return errors.New("сканирование уже выполняется")
	}
	cc := c.cfg.Get()
	pts := scan.Build(cc.Scan)
	if len(pts) == 0 {
		c.mu.Unlock()
		return errors.New("маршрут пуст")
	}
	c.scanPts = pts
	c.scanIdx = 0
	c.trail = nil
	c.scanRunning = true
	c.scanPaused = false
	ch := make(chan struct{})
	c.scanCancel = ch
	c.mu.Unlock()

	c.SetUDP(true)
	go c.scanLoop(pts, ch)
	return nil
}

func (c *Controller) stopScan() {
	c.mu.Lock()
	ch := c.scanCancel
	c.scanCancel = nil
	c.scanRunning = false
	c.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

func (c *Controller) StopScan() {
	c.stopScan()
}

func (c *Controller) PauseScan(paused bool) {
	c.mu.Lock()
	c.scanPaused = paused
	c.mu.Unlock()
}

func (c *Controller) ResetScan() {
	c.mu.Lock()
	c.trail = nil
	c.scanIdx = 0
	c.mu.Unlock()
}

func (c *Controller) scanLoop(pts []scan.Point, cancel chan struct{}) {
	defer func() {
		c.mu.Lock()
		c.scanRunning = false
		c.scanCancel = nil
		c.mu.Unlock()
	}()

	cc := c.cfg.Get()
	tol := 0.5 // мм

	for i, p := range pts {
		// пауза
		for {
			c.mu.RLock()
			paused := c.scanPaused
			c.mu.RUnlock()
			if !paused {
				break
			}
			select {
			case <-cancel:
				return
			case <-time.After(50 * time.Millisecond):
			}
		}

		select {
		case <-cancel:
			return
		default:
		}

		if dx := c.ax["X"]; dx != nil {
			_ = dx.SetProfileSpeed(cc.Scan.SpeedX)
			_ = dx.MoveTo(p.X)
		}
		if dy := c.ax["Y"]; dy != nil {
			_ = dy.SetProfileSpeed(cc.Scan.SpeedY)
			_ = dy.MoveTo(p.Y)
		}

		deadline := time.Now().Add(5 * time.Minute)
		for {
			select {
			case <-cancel:
				return
			case <-time.After(20 * time.Millisecond):
			}
			if c.atTarget(p, tol) || time.Now().After(deadline) {
				break
			}
		}

		c.mu.Lock()
		c.scanIdx = i + 1
		c.trail = append(c.trail, p)
		if len(c.trail) > 5000 {
			c.trail = c.trail[len(c.trail)-5000:]
		}
		c.mu.Unlock()
	}
}

func (c *Controller) atTarget(p scan.Point, tol float64) bool {
	if d := c.ax["X"]; d != nil {
		if math.Abs(d.State().Position-p.X) > tol {
			return false
		}
	}
	if d := c.ax["Y"]; d != nil {
		if math.Abs(d.State().Position-p.Y) > tol {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Состояние для веб-интерфейса
// ---------------------------------------------------------------------------

func (c *Controller) State() State {
	c.mu.RLock()
	defer c.mu.RUnlock()

	s := State{
		Connected: c.connected,
		ServerUp:  c.serverUp,
		ClientUp:  c.clientUp,
		LinkErr:   c.linkErr,
		UDPOn:     c.udpOn,
		Scan: ScanInfo{
			Running: c.scanRunning,
			Paused:  c.scanPaused,
			Index:   c.scanIdx,
			Total:   len(c.scanPts),
		},
		Trail: append([]scan.Point(nil), c.trail...),
	}
	if c.sender != nil {
		sent, last := c.sender.Stats()
		s.UDPSent = sent
		if !last.IsZero() {
			s.UDPLast = last.UnixMilli()
		}
	}

	for _, name := range c.order {
		if d := c.ax[name]; d != nil {
			s.Drives = append(s.Drives, d.State())
		}
	}
	for _, d := range s.Drives {
		switch strings.ToUpper(d.Name) {
		case "X":
			s.PosX = d.Position
		case "Y":
			s.PosY = d.Position
		}
	}

	// текущее положение — последняя точка «пройденного пути»
	if c.scanRunning {
		s.Trail = append(s.Trail, scan.Point{X: s.PosX, Y: s.PosY})
	}
	return s
}
