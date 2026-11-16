package tcp

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

type Client struct {
	mu      sync.Mutex
	addr    string
	timeout time.Duration
	conn    net.Conn
	txID    uint16
}

func New(addr string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 500 * time.Millisecond
	}
	return &Client{addr: addr, timeout: timeout}
}

func (c *Client) SetAddr(addr string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.addr == addr {
		return
	}
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
	c.addr = addr
}

func (c *Client) Addr() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.addr
}

func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
}

func (c *Client) dialLocked() error {
	conn, err := net.DialTimeout("tcp", c.addr, c.timeout)
	if err != nil {
		return err
	}
	c.conn = conn
	return nil
}

// Request отправляет PDU и возвращает тело ответа (PDU без MBAP).
func (c *Client) Request(unit byte, pdu []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		if err := c.dialLocked(); err != nil {
			return nil, err
		}
	}

	fail := func(err error) ([]byte, error) {
		if c.conn != nil {
			_ = c.conn.Close()
			c.conn = nil
		}
		return nil, err
	}

	c.txID++
	frame := make([]byte, 7+len(pdu))
	binary.BigEndian.PutUint16(frame[0:2], c.txID)
	binary.BigEndian.PutUint16(frame[2:4], 0)
	binary.BigEndian.PutUint16(frame[4:6], uint16(len(pdu)+1))
	frame[6] = unit
	copy(frame[7:], pdu)

	_ = c.conn.SetDeadline(time.Now().Add(c.timeout))
	if _, err := c.conn.Write(frame); err != nil {
		return fail(err)
	}

	hdr := make([]byte, 7)
	if _, err := io.ReadFull(c.conn, hdr); err != nil {
		return fail(err)
	}
	length := binary.BigEndian.Uint16(hdr[4:6])
	if length < 2 || length > 260 {
		return fail(fmt.Errorf("modbus: неверная длина ответа %d", length))
	}
	body := make([]byte, int(length)-1)
	if _, err := io.ReadFull(c.conn, body); err != nil {
		return fail(err)
	}
	if body[0]&0x80 != 0 {
		code := byte(0)
		if len(body) > 1 {
			code = body[1]
		}
		return nil, fmt.Errorf("modbus: исключение 0x%02x", code)
	}
	return body, nil
}