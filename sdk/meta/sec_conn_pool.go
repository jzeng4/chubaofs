// Copyright 2018 The Chubao Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
// implied. See the License for the specific language governing
// permissions and limitations under the License.

package meta

import (
	"net"
	"sync"
	"time"
)

/*
// WriteToConn writes through the given connection.
func (p *Packet) WriteToConn(c net.Conn) (err error) {
	c.SetWriteDeadline(time.Now().Add(WriteDeadlineTime * time.Second))
	header, err := Buffers.Get(util.PacketHeaderSize)
	if err != nil {
		header = make([]byte, util.PacketHeaderSize)
	}
	defer Buffers.Put(header)

	p.MarshalHeader(header)
	if _, err = c.Write(header); err == nil {
		if _, err = c.Write(p.Arg[:int(p.ArgLen)]); err == nil {
			if p.Data != nil && p.Size != 0 {
				_, err = c.Write(p.Data[:p.Size])
			}
		}
	}

	return
}


func (p *Pool) send(req *proto.Packet, c *net.TCPConn, msg string) (resp *proto.Packet, err error) {
	err = req.WriteToConn(c)
	if err != nil {
		return nil, errors.Trace(err, "%s: Failed to write to conn, req(%v)", msg, req)
	}
	resp = proto.NewPacket()
	err = resp.ReadFromConn(c, proto.ReadDeadlineTime)
	if err != nil {
		return nil, errors.Trace(err, "% Failed to read from conn, req(%v)", msg, req)
	}
	return resp, nil
}

func (p *Pool) secHandShake(c *net.TCPConn) (err error) {
	var (
		err  error
		resp *proto.Packet
	)

	reqHello := &proto.ClientHelloRequest{}
	packet := proto.NewPacketReqID()
	packet.Opcode = proto.OpClientHello
	err = packet.MarshalData(reqHello)
	if err != nil {
		//log.LogErrorf("clientHello: err(%v)", err)
		return
	}
	resp, err = p.send(packet, c, "clientHello")
	if err != nil {
		return
	}

	//log.LogInfof("jzeng successfully")

	//TODO verify ReadResponse

	req := &proto.ClientVerifyRequest{}
	packet := proto.NewPacketReqID()
	packet.Opcode = proto.OpClientVerify
	err = packet.MarshalData(req)
	if err != nil {
		log.LogErrorf("clientHello: err(%v)", err)
		return
	}
	resp, err = p.send(packet, c, "clientHello")
	if err != nil {
		return
	}

}*/
type SecObject struct {
	conn    *net.TCPConn
	idle    int64
	texp    int64
	version string
}

type SecConn struct {
	conn    *net.TCPConn
	texp    int64
	version string
}

const (
	SecConnectIdleTime = 30
)

type SecConnectPool struct {
	sync.RWMutex
	pools   map[string]*SecPool
	mincap  int
	maxcap  int
	timeout int64
	version string
	texp    int64
}

func NewConnectPool(version string, texp int64) (cp *SecConnectPool) {
	cp = &SecConnectPool{pools: make(map[string]*SecPool), mincap: 5, maxcap: 80, timeout: int64(time.Second * SecConnectIdleTime), version: version, texp: texp}
	go cp.autoRelease()

	return cp
}

/*func SecDailTimeOut(target string, timeout time.Duration) (c *net.TCPConn, err error) {
	var connect net.Conn
	connect, err = net.DialTimeout("tcp", target, timeout)
	if err == nil {
		conn := connect.(*net.TCPConn)
		conn.SetKeepAlive(true)
		conn.SetNoDelay(true)
		c = conn
	}
	return
}*/

func (cp *SecConnectPool) GetConnect(targetAddr string) (c *SecConn, err error) {
	cp.RLock()
	pool, ok := cp.pools[targetAddr]
	cp.RUnlock()
	if !ok {
		cp.Lock()
		pool = NewPool(cp.mincap, cp.maxcap, cp.timeout, targetAddr, cp.version, cp.texp)
		cp.pools[targetAddr] = pool
		cp.Unlock()
	}

	return pool.GetConnectFromPool()
}

func (cp *SecConnectPool) PutConnect(c *SecConn, forceClose bool) {
	if c == nil {
		return
	}
	if forceClose {
		c.conn.Close()
		return
	}
	addr := c.conn.RemoteAddr().String()
	cp.RLock()
	pool, ok := cp.pools[addr]
	cp.RUnlock()
	if !ok {
		c.conn.Close()
		return
	}
	object := &SecObject{conn: c.conn, idle: time.Now().UnixNano(), texp: c.texp, version: c.version}
	pool.PutConnectObjectToPool(object)

	return
}

func (cp *SecConnectPool) autoRelease() {
	for {
		pools := make([]*SecPool, 0)
		cp.RLock()
		for _, pool := range cp.pools {
			pools = append(pools, pool)
		}
		cp.RUnlock()
		for _, pool := range pools {
			pool.autoRelease()
		}
		time.Sleep(time.Second)
	}

}

type SecPool struct {
	objects chan *SecObject
	mincap  int
	maxcap  int
	target  string
	timeout int64
	version string
	texp    int64
}

func NewPool(min, max int, timeout int64, target string, version string, texp int64) (p *SecPool) {
	p = new(SecPool)
	p.mincap = min
	p.maxcap = max
	p.target = target
	p.objects = make(chan *SecObject, max)
	p.timeout = timeout
	p.version = version
	p.texp = texp
	p.initAllConnect()
	return p
}

func (p *SecPool) initAllConnect() {
	for i := 0; i < p.mincap; i++ {
		c, err := net.Dial("tcp", p.target)
		if err == nil {
			conn := c.(*net.TCPConn)
			conn.SetKeepAlive(true)
			conn.SetNoDelay(true)
			o := &SecObject{conn: conn, idle: time.Now().UnixNano(), version: p.version, texp: p.texp}
			p.PutConnectObjectToPool(o)
		}
	}
}

func (p *SecPool) PutConnectObjectToPool(o *SecObject) {
	select {
	case p.objects <- o:
		return
	default:
		if o.conn != nil {
			o.conn.Close()
		}
		return
	}
}

func (p *SecPool) autoRelease() {
	connectLen := len(p.objects)
	for i := 0; i < connectLen; i++ {
		select {
		case o := <-p.objects:
			if time.Now().UnixNano()-int64(o.idle) > p.timeout {
				o.conn.Close()
			} else {
				p.PutConnectObjectToPool(o)
			}
		default:
			return
		}
	}
}

func (p *SecPool) NewConnect(target string) (c *SecConn, err error) {
	var connect net.Conn
	connect, err = net.Dial("tcp", p.target)
	if err == nil {
		conn := connect.(*net.TCPConn)
		conn.SetKeepAlive(true)
		conn.SetNoDelay(true)
		c = &SecConn{conn: conn, texp: p.texp, version: p.version}
	}
	return
}

func (p *SecPool) GetConnectFromPool() (c *SecConn, err error) {
	var (
		o *SecObject
	)
	for i := 0; i < len(p.objects); i++ {
		select {
		case o = <-p.objects:
			if time.Now().UnixNano()-int64(o.idle) > p.timeout || time.Now().Unix() > int64(o.texp) {
				o.conn.Close()
				o = nil
				break
			}
			return &SecConn{conn: o.conn, texp: o.texp, version: o.version}, nil
		default:
			return p.NewConnect(p.target)
		}
	}

	return p.NewConnect(p.target)
}
