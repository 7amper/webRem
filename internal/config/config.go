package config

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
)

// ---------------------------------------------------------------------------
// Модель конфигурации
// ---------------------------------------------------------------------------

type Config struct {
	Link   Link    `json:"link"`
	Modbus Modbus  `json:"modbus"`
	Drives []Drive `json:"drives"`
	Scan   Scan    `json:"scan"`
	UI     UI      `json:"ui"`
}

// Link — фрейм «Связь»
type Link struct {
	HTTPAddr      string  `json:"httpAddr"`      // адрес TCP [127.0.0.1:3000]
	UDPAddr       string  `json:"udpAddr"`       // адрес UDP [10.10.10.17:5000]
	PollHz        int     `json:"pollHz"`        // периодичность опроса [30 раз в секунду]
	UDPIntervalMM float64 `json:"udpIntervalMm"` // отправлять не реже чем через 0.5 мм
	UDPEndian     string  `json:"udpEndian"`     // little | big
}

// Modbus — фрейм «Modbus»
type Modbus struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	TimeoutMs  int    `json:"timeoutMs"`
	WordOrder  string `json:"wordOrder"`  // big | little — порядок слов в 32-битных значениях
	AddrOffset int    `json:"addrOffset"` // смещение адресов (0 по умолчанию)

	// --- адреса управления ---
	RegEnable          uint16 `json:"regEnable"`          // 0x1000
	RegCurrentSet      uint16 `json:"regCurrentSet"`      // 0x1001
	RegSpeedSet        uint16 `json:"regSpeedSet"`        // 0x1002 (32)
	RegAccelerationSet uint16 `json:"regAccelerationSet"` // 0x1004 (32)
	RegDecelerationSet uint16 `json:"regDecelerationSet"` // 0x1006 (32)
	RegPositionSet     uint16 `json:"regPositionSet"`     // 0x1008 (32)
	RegOperationMode   uint16 `json:"regOperationMode"`   // 0x100A
	RegOutput          uint16 `json:"regOutput"`          // 0x1302

	// --- адреса чтения ---
	RegInput           uint16 `json:"regInput"`           // 0x1301
	RegOutputRead      uint16 `json:"regOutputRead"`      // 0x1302
	RegRealtimeFault1  uint16 `json:"regRealtimeFault1"`  // 0x1303
	RegRealtimeStatus1 uint16 `json:"regRealtimeStatus1"` // 0x1304
	RegCurrentGet      uint16 `json:"regCurrentGet"`      // 0x1305
	RegSpeedGet        uint16 `json:"regSpeedGet"`        // 0x1306 (32)
	RegPositionGet     uint16 `json:"regPositionGet"`     // 0x1308 (32)
}

// Drive — фрейм «Привода»
type Drive struct {
	Name      string  `json:"name"`      // X | Y
	UnitID    byte    `json:"unitId"`    // 1 (X) | 2 (Y)
	MaxSpeed  float64 `json:"maxSpeed"`  // мм/сек
	MinSpeed  float64 `json:"minSpeed"`  // мм/сек
	Gear      float64 `json:"gear"`      // передаточный коэффициент положения, имп/мм
	SpeedCoef float64 `json:"speedCoef"` // ед. привода на мм/сек
	Accel     float64 `json:"accel"`
	Decel     float64 `json:"decel"`
	Invert    bool    `json:"invert"`
	OffsetMM  float64 `json:"offsetMm"` // статическая поправка привязки
}

// Scan — фрейм «Построчное сканирование» / вкладка «Задание сканирования»
type Scan struct {
	Orientation  string  `json:"orientation"` // horizontal | vertical
	PipeLength   float64 `json:"pipeLength"`  // мм
	PipeDiameter float64 `json:"pipeDiameter"`

	StartX float64 `json:"startX"`
	StartY float64 `json:"startY"`
	RangeX float64 `json:"rangeX"`
	RangeY float64 `json:"rangeY"`
	Step   float64 `json:"step"`
	SpeedX float64 `json:"speedX"`
	SpeedY float64 `json:"speedY"`

	Direction string `json:"direction"` // along | across
}

type UI struct {
	Theme string `json:"theme"` // dark | light
}

// ---------------------------------------------------------------------------
// Значения по умолчанию
// ---------------------------------------------------------------------------

func Default() Config {
	return Config{
		Link: Link{
			HTTPAddr:      "127.0.0.1:3000",
			UDPAddr:       "10.10.10.17:5000",
			PollHz:        30,
			UDPIntervalMM: 0.5,
			UDPEndian:     "little",
		},
		Modbus: Modbus{
			Host: "192.168.1.10", Port: 502, TimeoutMs: 300,
			WordOrder: "big", AddrOffset: 0,
			RegEnable:          0x1000,
			RegCurrentSet:      0x1001,
			RegSpeedSet:        0x1002,
			RegAccelerationSet: 0x1004,
			RegDecelerationSet: 0x1006,
			RegPositionSet:     0x1008,
			RegOperationMode:   0x100A,
			RegOutput:          0x1302,
			RegInput:           0x1301,
			RegOutputRead:      0x1302,
			RegRealtimeFault1:  0x1303,
			RegRealtimeStatus1: 0x1304,
			RegCurrentGet:      0x1305,
			RegSpeedGet:        0x1306,
			RegPositionGet:     0x1308,
		},
		Drives: []Drive{
			{Name: "X", UnitID: 1, MaxSpeed: 100, MinSpeed: 1, Gear: 5, SpeedCoef: 1, Accel: 1000, Decel: 1000},
			{Name: "Y", UnitID: 2, MaxSpeed: 100, MinSpeed: 1, Gear: 5, SpeedCoef: 1, Accel: 1000, Decel: 1000},
		},
		Scan: Scan{
			Orientation:  "horizontal",
			PipeLength:   1000,
			PipeDiameter: 500,
			StartX:       -100,
			StartY:       -100,
			RangeX:       500,
			RangeY:       500,
			Step:         10,
			SpeedX:       10,
			SpeedY:       10,
			Direction:    "along",
		},
		UI: UI{Theme: "dark"},
	}
}

func (c *Config) normalize() {
	d := Default()
	if c.Link.HTTPAddr == "" {
		c.Link.HTTPAddr = d.Link.HTTPAddr
	}
	if c.Link.UDPAddr == "" {
		c.Link.UDPAddr = d.Link.UDPAddr
	}
	if c.Link.PollHz <= 0 {
		c.Link.PollHz = d.Link.PollHz
	}
	if c.Link.UDPIntervalMM <= 0 {
		c.Link.UDPIntervalMM = d.Link.UDPIntervalMM
	}
	if c.Link.UDPEndian == "" {
		c.Link.UDPEndian = d.Link.UDPEndian
	}
	if c.Modbus.Host == "" {
		c.Modbus.Host = d.Modbus.Host
	}
	if c.Modbus.Port == 0 {
		c.Modbus.Port = d.Modbus.Port
	}
	if c.Modbus.TimeoutMs <= 0 {
		c.Modbus.TimeoutMs = d.Modbus.TimeoutMs
	}
	if c.Modbus.WordOrder == "" {
		c.Modbus.WordOrder = d.Modbus.WordOrder
	}
	if len(c.Drives) != 2 {
		c.Drives = d.Drives
	}
	for i := range c.Drives {
		if c.Drives[i].Gear == 0 {
			c.Drives[i].Gear = 1
		}
		if c.Drives[i].SpeedCoef == 0 {
			c.Drives[i].SpeedCoef = 1
		}
		if c.Drives[i].MaxSpeed <= 0 {
			c.Drives[i].MaxSpeed = 100
		}
		if c.Drives[i].MinSpeed <= 0 {
			c.Drives[i].MinSpeed = 1
		}
		if c.Drives[i].Name == "" {
			c.Drives[i].Name = []string{"X", "Y"}[i]
		}
	}
	if c.Scan.PipeLength <= 0 {
		c.Scan.PipeLength = d.Scan.PipeLength
	}
	if c.Scan.PipeDiameter <= 0 {
		c.Scan.PipeDiameter = d.Scan.PipeDiameter
	}
	if c.Scan.Step <= 0 {
		c.Scan.Step = d.Scan.Step
	}
	if c.Scan.Orientation == "" {
		c.Scan.Orientation = d.Scan.Orientation
	}
	if c.Scan.Direction == "" {
		c.Scan.Direction = d.Scan.Direction
	}
	if c.UI.Theme == "" {
		c.UI.Theme = d.UI.Theme
	}
}

// ---------------------------------------------------------------------------
// Менеджер конфигурации (потокобезопасный, с сохранением в JSON)
// ---------------------------------------------------------------------------

type Manager struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

func NewManager(path string) (*Manager, error) {
	m := &Manager{path: path}

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		m.cfg = Default()
		m.cfg.normalize()
		if err := m.saveLocked(m.cfg); err != nil {
			return nil, err
		}
		return m, nil
	}

	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	c.normalize()
	m.cfg = c
	return m, nil
}

func (m *Manager) Get() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c := m.cfg
	c.Drives = append([]Drive(nil), m.cfg.Drives...)
	return c
}

func (m *Manager) Set(c Config) error {
	c.normalize()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.saveLocked(c); err != nil {
		return err
	}
	m.cfg = c
	return nil
}

func (m *Manager) Path() string { return m.path }

func (m *Manager) saveLocked(c Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, m.path); err != nil {
		return errors.New("не удалось сохранить конфигурацию: " + err.Error())
	}
	return nil
}