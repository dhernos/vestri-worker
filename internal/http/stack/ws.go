package stack

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	wsOpcodeContinuation = 0x0
	wsOpcodeText         = 0x1
	wsOpcodeBinary       = 0x2
	wsOpcodeClose        = 0x8
	wsOpcodePing         = 0x9
	wsOpcodePong         = 0xA

	maxWSFramePayload = 1 << 20
)

type wsConn struct {
	conn    net.Conn
	rw      *bufio.ReadWriter
	writeMu sync.Mutex
}

type wsUpgradeError struct {
	StatusCode int
	Message    string
}

func (e *wsUpgradeError) Error() string {
	return e.Message
}

func upgradeWebSocket(w http.ResponseWriter, r *http.Request) (*wsConn, error) {
	if !headerContainsToken(r.Header, "Connection", "upgrade") || !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		return nil, &wsUpgradeError{
			StatusCode: http.StatusBadRequest,
			Message:    "websocket upgrade required",
		}
	}

	if version := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Version")); version != "13" {
		return nil, &wsUpgradeError{
			StatusCode: http.StatusUpgradeRequired,
			Message:    "unsupported websocket version",
		}
	}

	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if key == "" {
		return nil, &wsUpgradeError{
			StatusCode: http.StatusBadRequest,
			Message:    "missing websocket key",
		}
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return nil, fmt.Errorf("http server does not support websocket hijacking")
	}

	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return nil, err
	}

	accept := websocketAcceptValue(key)
	if _, err := rw.WriteString("HTTP/1.1 101 Switching Protocols\r\n"); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if _, err := rw.WriteString("Upgrade: websocket\r\n"); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if _, err := rw.WriteString("Connection: Upgrade\r\n"); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if _, err := rw.WriteString("Sec-WebSocket-Accept: " + accept + "\r\n"); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if _, err := rw.WriteString("\r\n"); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := rw.Flush(); err != nil {
		_ = conn.Close()
		return nil, err
	}

	return &wsConn{
		conn: conn,
		rw:   rw,
	}, nil
}

func websocketAcceptValue(key string) string {
	hash := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(hash[:])
}

func (c *wsConn) ReadFrame() (byte, []byte, error) {
	firstByte, err := c.rw.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	secondByte, err := c.rw.ReadByte()
	if err != nil {
		return 0, nil, err
	}

	fin := firstByte&0x80 != 0
	opcode := firstByte & 0x0F
	if !fin {
		return 0, nil, fmt.Errorf("fragmented websocket frames are unsupported")
	}
	if opcode == wsOpcodeContinuation {
		return 0, nil, fmt.Errorf("continuation websocket frames are unsupported")
	}

	masked := secondByte&0x80 != 0
	payloadLen := int64(secondByte & 0x7F)
	if !masked {
		return 0, nil, fmt.Errorf("unmasked websocket payloads are unsupported")
	}
	switch payloadLen {
	case 126:
		var extended uint16
		if err := binary.Read(c.rw, binary.BigEndian, &extended); err != nil {
			return 0, nil, err
		}
		payloadLen = int64(extended)
	case 127:
		var extended uint64
		if err := binary.Read(c.rw, binary.BigEndian, &extended); err != nil {
			return 0, nil, err
		}
		payloadLen = int64(extended)
	}

	if payloadLen < 0 || payloadLen > maxWSFramePayload {
		return 0, nil, fmt.Errorf("websocket payload exceeds limit")
	}

	var maskingKey [4]byte
	if masked {
		if _, err := io.ReadFull(c.rw, maskingKey[:]); err != nil {
			return 0, nil, err
		}
	}

	payload := make([]byte, payloadLen)
	if payloadLen > 0 {
		if _, err := io.ReadFull(c.rw, payload); err != nil {
			return 0, nil, err
		}
	}

	if masked {
		for i := range payload {
			payload[i] ^= maskingKey[i%4]
		}
	}

	return opcode, payload, nil
}

func (c *wsConn) WriteFrame(opcode byte, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	frame := make([]byte, 0, len(payload)+14)
	frame = append(frame, 0x80|opcode)

	payloadLen := len(payload)
	switch {
	case payloadLen <= 125:
		frame = append(frame, byte(payloadLen))
	case payloadLen <= 65535:
		frame = append(frame, 126)
		var size [2]byte
		binary.BigEndian.PutUint16(size[:], uint16(payloadLen))
		frame = append(frame, size[:]...)
	default:
		frame = append(frame, 127)
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(payloadLen))
		frame = append(frame, size[:]...)
	}

	frame = append(frame, payload...)
	if _, err := c.rw.Write(frame); err != nil {
		return err
	}
	return c.rw.Flush()
}

func (c *wsConn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

func (c *wsConn) Close() error {
	return c.conn.Close()
}

func headerContainsToken(header http.Header, key, token string) bool {
	token = strings.ToLower(strings.TrimSpace(token))
	for _, value := range header.Values(key) {
		for _, piece := range strings.Split(value, ",") {
			if strings.ToLower(strings.TrimSpace(piece)) == token {
				return true
			}
		}
	}
	return false
}
