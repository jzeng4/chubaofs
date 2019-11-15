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

package util

import (
	"net"
	"sync"
	"time"
)

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
}

func NewSecConnectPool() (cp *SecConnectPool) {
	cp = &SecConnectPool{pools: make(map[string]*SecPool), mincap: 5, maxcap: 80, timeout: int64(time.Second * SecConnectIdleTime)}
	go cp.autoSecRelease()

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

func (cp *SecConnectPool) GetSecConnect(targetAddr string) (c *SecConn, err error) {
	cp.RLock()
	pool, ok := cp.pools[targetAddr]
	cp.RUnlock()
	if !ok {
		cp.Lock()
		pool = NewSecPool(cp.mincap, cp.maxcap, cp.timeout, targetAddr)
		cp.pools[targetAddr] = pool
		cp.Unlock()
	}

	return pool.GetSecConnectFromPool()
}

func (cp *SecConnectPool) PutSecConnect(c *net.TCPConn, forceClose bool) {
	if c == nil {
		return
	}
	if forceClose {
		c.Close()
		return
	}
	addr := c.RemoteAddr().String()
	cp.RLock()
	pool, ok := cp.pools[addr]
	cp.RUnlock()
	if !ok {
		c.Close()
		return
	}
	object := &SecObject{conn: c, idle: time.Now().UnixNano()}
	pool.PutSecConnectObjectToPool(object)

	return
}

func (cp *SecConnectPool) autoSecRelease() {
	for {
		pools := make([]*SecPool, 0)
		cp.RLock()
		for _, pool := range cp.pools {
			pools = append(pools, pool)
		}
		cp.RUnlock()
		for _, pool := range pools {
			pool.autoSecRelease()
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
}

func NewSecPool(min, max int, timeout int64, target string) (p *SecPool) {
	p = new(SecPool)
	p.mincap = min
	p.maxcap = max
	p.target = target
	p.objects = make(chan *SecObject, max)
	p.timeout = timeout
	p.initAllSecConnect()
	return p
}

func (p *SecPool) initAllSecConnect() {
	for i := 0; i < p.mincap; i++ {
		c, err := net.Dial("tcp", p.target)
		if err == nil {
			conn := c.(*net.TCPConn)
			conn.SetKeepAlive(true)
			conn.SetNoDelay(true)
			o := &SecObject{conn: conn, idle: time.Now().UnixNano()}
			p.PutSecConnectObjectToPool(o)
		}
	}
}

func (p *SecPool) PutSecConnectObjectToPool(o *SecObject) {
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

func (p *SecPool) autoSecRelease() {
	connectLen := len(p.objects)
	for i := 0; i < connectLen; i++ {
		select {
		case o := <-p.objects:
			if time.Now().UnixNano()-int64(o.idle) > p.timeout {
				o.conn.Close()
			} else {
				p.PutSecConnectObjectToPool(o)
			}
		default:
			return
		}
	}
}

func (p *SecPool) NewSecConnect(target string) (c *SecConn, err error) {
	var connect net.Conn
	connect, err = net.Dial("tcp", p.target)
	if err == nil {
		conn := connect.(*net.TCPConn)
		conn.SetKeepAlive(true)
		conn.SetNoDelay(true)
		c = &SecConn{conn: conn, texp: p.texp, version: o.version}
	}
	return
}

func (p *SecPool) GetSecConnectFromPool() (c *SecConn, err error) {
	var (
		o *SecObject
	)
	for i := 0; i < len(p.objects); i++ {
		select {
		case o = <-p.objects:
			if time.Now().UnixNano()-int64(o.idle) > p.timeout {
				o.conn.Close()
				o = nil
				break
			}
			return &SecConn{conn: o.conn, texp: o.texp, version: o.version}, nil
		default:
			return p.NewSecConnect(p.target)
		}
	}

	return p.NewSecConnect(p.target)
}
