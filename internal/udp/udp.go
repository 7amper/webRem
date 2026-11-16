package udp

import (
	"encoding/binary"
	"net"
	"sync"
	"time"
)

const (
	Header  uint32 = 0x5a5a5a5a
	Trailer uint32 = 0xa5a5a5a5
	Scale          = 10 // множитель позиции
)

type Sender struct {
	mu       sync.Mutex
	addr     string
	conn     *net.UDPConn
	order    binary.ByteOrder
	sent     uint64
	lastTime time.Time
	minGap   time.Duration
}

func New(addr string, order binary.ByteOrder) (*Sender, error) {
	ua, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialUDP("udp4", nil, ua)
	if err != nil {
		return nil, err
	}
	return &Sender{
		addr:   addr,
		conn:   conn,
		order:  order,
		minGap: 5 * time.Millisecond,
	}, nil
}

func (s *Sender) Addr() string { return s.addr }

func (s *Sender) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		err := s.conn.Close()
		s.conn = nil
		return err
	}
	return nil
}

// Send — посылка 16 байт: header, X*10, Y*10, trailer
func (s *Sender) Send(xMM, yMM float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return net.ErrClosed
	}
	buf := make([]byte, 16)
	o := s.order
	o.PutUint32(buf[0:], Header)
	o.PutUint32(buf[4:], uint32(int32(xMM*Scale)))
	o.PutUint32(buf[8:], uint32(int32(yMM*Scale)))
	o.PutUint32(buf[12:], Trailer)

	if _, err := s.conn.Write(buf); err != nil {
		return err
	}
	s.sent++
	s.lastTime = time.Now()
	return nil
}

func (s *Sender) Stats() (uint64, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sent, s.lastTime
}