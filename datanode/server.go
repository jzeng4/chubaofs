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

package datanode

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"errors"
	"os"
	"syscall"

	"github.com/chubaofs/chubaofs/proto"
	"github.com/chubaofs/chubaofs/raftstore"
	"github.com/chubaofs/chubaofs/repl"
	"github.com/chubaofs/chubaofs/util"
	"github.com/chubaofs/chubaofs/util/config"
	"github.com/chubaofs/chubaofs/util/exporter"
	"github.com/chubaofs/chubaofs/util/log"
)

var (
	ErrIncorrectStoreType       = errors.New("Incorrect store type")
	ErrNoSpaceToCreatePartition = errors.New("No disk space to create a data partition")
	ErrNewSpaceManagerFailed    = errors.New("Creater new space manager failed")

	LocalIP      string
	gConnPool    = util.NewConnectPool()
	MasterHelper = util.NewMasterHelper()
)

const (
	DefaultRackName         = "cfs_rack1"
	DefaultRaftDir          = "raft"
	DefaultRaftLogsToRetain = 10 // Count of raft logs per data partition
	DefaultDiskMaxErr       = 1
	DefaultDiskRetainMin    = 5 * util.GB // GB
	DefaultDiskRetainMax    = 30 * util.GB // GB
)

const (
	ModuleName = "dataNode"
)

const (
	ConfigKeyLocalIP       = "localIP"       // string
	ConfigKeyPort          = "port"          // int
	ConfigKeyMasterAddr    = "masterAddr"    // array
	ConfigKeyRack          = "rack"          // string
	ConfigKeyDisks         = "disks"         // array
	ConfigKeyRaftDir       = "raftDir"       // string
	ConfigKeyRaftHeartbeat = "raftHeartbeat" // string
	ConfigKeyRaftReplica   = "raftReplica"   // string
)

// DataNode defines the structure of a data node.
type DataNode struct {
	space           *SpaceManager
	port            string
	rackName        string
	clusterID       string
	localIP         string
	localServerAddr string
	nodeID          uint64
	raftDir         string
	raftHeartbeat   string
	raftReplica     string
	raftStore       raftstore.RaftStore
	tcpListener     net.Listener
	stopC           chan bool
	state           uint32
	wg              sync.WaitGroup
}

func NewServer() *DataNode {
	return &DataNode{}
}

func (s *DataNode) Start(cfg *config.Config) (err error) {
	runtime.GOMAXPROCS(runtime.NumCPU())
	if atomic.CompareAndSwapUint32(&s.state, Standby, Start) {
		defer func() {
			if err != nil {
				atomic.StoreUint32(&s.state, Standby)
			} else {
				atomic.StoreUint32(&s.state, Running)
			}
		}()
		if err = s.onStart(cfg); err != nil {
			return
		}
		s.wg.Add(1)
	}
	return
}

// Shutdown shuts down the current data node.
func (s *DataNode) Shutdown() {
	if atomic.CompareAndSwapUint32(&s.state, Running, Shutdown) {
		s.onShutdown()
		s.wg.Done()
		atomic.StoreUint32(&s.state, Stopped)
	}
}

// Sync keeps data node in sync.
func (s *DataNode) Sync() {
	if atomic.LoadUint32(&s.state) == Running {
		s.wg.Wait()
	}
}

// Workflow of starting up a data node.
func (s *DataNode) onStart(cfg *config.Config) (err error) {
	s.stopC = make(chan bool, 0)

	// parse the config file
	if err = s.parseConfig(cfg); err != nil {
		return
	}

	exporter.Init(ModuleName, cfg)
	s.register(cfg)

	// start the raft server
	if err = s.startRaftServer(cfg); err != nil {
		return
	}

	// create space manager (disk, partition, etc.)
	if err = s.startSpaceManager(cfg); err != nil {
		return
	}

	// check local partition compare with master ,if lack,then not start
	if err = s.checkLocalPartitionMatchWithMaster(); err != nil {
		fmt.Println(err)
		exporter.Warning(err.Error())
		return
	}

	// start tcp listening
	if err = s.startTCPService(); err != nil {
		return
	}
	go s.registerHandler()

	return
}

func (s *DataNode) onShutdown() {
	close(s.stopC)
	s.stopTCPService()
	s.stopRaftServer()
	return
}

func (s *DataNode) parseConfig(cfg *config.Config) (err error) {
	var (
		port       string
		regexpPort *regexp.Regexp
	)
	LocalIP = cfg.GetString(ConfigKeyLocalIP)
	port = cfg.GetString(ConfigKeyPort)
	if regexpPort, err = regexp.Compile("^(\\d)+$"); err != nil {
		return fmt.Errorf("Err:no port")
	}
	if !regexpPort.MatchString(port) {
		return fmt.Errorf("Err:port must string")
	}
	s.port = port
	if len(cfg.GetArray(ConfigKeyMasterAddr)) == 0 {
		return fmt.Errorf("Err:masterAddr unavalid")
	}
	for _, ip := range cfg.GetArray(ConfigKeyMasterAddr) {
		MasterHelper.AddNode(ip.(string))
	}
	s.rackName = cfg.GetString(ConfigKeyRack)
	if s.rackName == "" {
		s.rackName = DefaultRackName
	}
	log.LogDebugf("action[parseConfig] load masterAddrs(%v).", MasterHelper.Nodes())
	log.LogDebugf("action[parseConfig] load port(%v).", s.port)
	log.LogDebugf("action[parseConfig] load rackName(%v).", s.rackName)
	return
}

func (s *DataNode) startSpaceManager(cfg *config.Config) (err error) {
	s.space = NewSpaceManager(s.rackName)
	if err != nil || len(strings.TrimSpace(s.port)) == 0 {
		err = ErrNewSpaceManagerFailed
		return
	}

	s.space.SetRaftStore(s.raftStore)
	s.space.SetNodeID(s.nodeID)
	s.space.SetClusterID(s.clusterID)

	var wg sync.WaitGroup
	for _, d := range cfg.GetArray(ConfigKeyDisks) {
		log.LogDebugf("action[startSpaceManager] load disk raw config(%v).", d)

		// format "PATH:RESET_SIZE
		arr := strings.Split(d.(string), ":")
		if len(arr) != 2 {
			return errors.New("Invalid disk configuration. Example: PATH:RESERVE_SIZE")
		}
		path := arr[0]
		fileInfo, err := os.Stat(path)
		if err != nil {
			return errors.New(fmt.Sprintf("Stat disk path error: %s", err.Error()))
		}
		if !fileInfo.IsDir() {
			return errors.New("Disk path is not dir")
		}
		wg.Add(1)
		go func(wg *sync.WaitGroup, path string) {
			defer wg.Done()
			s.space.LoadDisk(path, DefaultDiskMaxErr)
		}(&wg, path)
	}
	wg.Wait()
	return nil
}

// registers the data node on the master to report the information such as IsIPV4 address.
// The startup of a data node will be blocked until the registration succeeds.
func (s *DataNode) register(cfg *config.Config) {
	var (
		err  error
		data []byte
	)

	timer := time.NewTimer(0)

	// get the IsIPV4 address, cluster ID and node ID from the master
	for {
		select {
		case <-timer.C:
			data, err = MasterHelper.Request(http.MethodGet, proto.AdminGetIP, nil, nil)
			masterAddr := MasterHelper.Leader()
			if err != nil {
				log.LogErrorf("action[registerToMaster] cannot get ip from master(%v) err(%v).",
					masterAddr, err)
				timer.Reset(2 * time.Second)
				continue
			}
			cInfo := new(proto.ClusterInfo)
			json.Unmarshal(data, cInfo)
			if LocalIP == "" {
				LocalIP = string(cInfo.Ip)
			}
			s.clusterID = cInfo.Cluster
			s.localServerAddr = fmt.Sprintf("%s:%v", LocalIP, s.port)
			if !util.IsIPV4(LocalIP) {
				log.LogErrorf("action[registerToMaster] got an invalid local ip(%v) from master(%v).",
					LocalIP, masterAddr)
				timer.Reset(2 * time.Second)
				continue
			}

			// register this data node on the master
			params := make(map[string]string)
			params["addr"] = fmt.Sprintf("%s:%v", LocalIP, s.port)
			data, err = MasterHelper.Request(http.MethodPost, proto.AddDataNode, params, nil)
			if err != nil {
				log.LogErrorf("action[registerToMaster] cannot register this node to master[%v] err(%v).",
					masterAddr, err)
				timer.Reset(2 * time.Second)
				continue
			}

			exporter.RegistConsul(s.clusterID, ModuleName, cfg)

			nodeID := strings.TrimSpace(string(data))
			s.nodeID, err = strconv.ParseUint(nodeID, 10, 64)
			log.LogDebugf("[tempDebug] nodeID(%v)", s.nodeID)
			return
		case <-s.stopC:
			timer.Stop()
			return
		}
	}
}

type DataNodeInfo struct {
	Addr                      string
	PersistenceDataPartitions []uint64
}

func (s *DataNode) checkLocalPartitionMatchWithMaster() (err error) {
	params := make(map[string]string)
	params["addr"] = s.localServerAddr
	var data interface{}
	for i := 0; i < 3; i++ {
		data, err = MasterHelper.Request(http.MethodGet, proto.GetDataNode, params, nil)
		if err != nil {
			log.LogErrorf("checkLocalPartitionMatchWithMaster error %v", err)
			continue
		}
		break
	}
	dinfo := new(DataNodeInfo)
	if err = json.Unmarshal(data.([]byte), dinfo); err != nil {
		err = fmt.Errorf("checkLocalPartitionMatchWithMaster jsonUnmarsh failed %v", err)
		log.LogErrorf(err.Error())
		return
	}
	if len(dinfo.PersistenceDataPartitions) == 0 {
		return
	}
	lackPartitions := make([]uint64, 0)
	for _, partitionID := range dinfo.PersistenceDataPartitions {
		dp := s.space.Partition(partitionID)
		if dp == nil {
			lackPartitions = append(lackPartitions, partitionID)
		}
	}
	if len(lackPartitions) == 0 {
		return
	}
	err = fmt.Errorf("LackPartitions %v on datanode %v,datanode cannot start", lackPartitions, s.localServerAddr)
	log.LogErrorf(err.Error())
	return
}

func (s *DataNode) registerHandler() {
	http.HandleFunc("/disks", s.getDiskAPI)
	http.HandleFunc("/partitions", s.getPartitionsAPI)
	http.HandleFunc("/partition", s.getPartitionAPI)
	http.HandleFunc("/extent", s.getExtentAPI)
	http.HandleFunc("/block", s.getBlockCrcAPI)
	http.HandleFunc("/stats", s.getStatAPI)
	http.HandleFunc("/raftStatus", s.getRaftStatus)
}

func (s *DataNode) startTCPService() (err error) {
	log.LogInfo("Start: startTCPService")
	addr := fmt.Sprintf(":%v", s.port)
	l, err := net.Listen(NetworkProtocol, addr)
	log.LogDebugf("action[startTCPService] listen %v address(%v).", NetworkProtocol, addr)
	if err != nil {
		log.LogError("failed to listen, err:", err)
		return
	}
	s.tcpListener = l
	go func(ln net.Listener) {
		for {
			conn, err := ln.Accept()
			if err != nil {
				log.LogErrorf("action[startTCPService] failed to accept, err:%s", err.Error())
				break
			}
			log.LogDebugf("action[startTCPService] accept connection from %s.", conn.RemoteAddr().String())
			go s.serveConn(conn)
		}
	}(l)
	return
}

func (s *DataNode) stopTCPService() (err error) {
	if s.tcpListener != nil {

		s.tcpListener.Close()
		log.LogDebugf("action[stopTCPService] stop tcp service.")
	}
	return
}

func (s *DataNode) serveConn(conn net.Conn) {
	space := s.space
	space.Stats().AddConnection()
	c, _ := conn.(*net.TCPConn)
	c.SetKeepAlive(true)
	c.SetNoDelay(true)
	packetProcessor := repl.NewReplProtocol(c, s.Prepare, s.OperatePacket, s.Post)
	packetProcessor.ServerConn()
}

// Increase the disk error count by one.
func (s *DataNode) incDiskErrCnt(partitionID uint64, err error, flag uint8) {
	if err == nil {
		return
	}
	dp := s.space.Partition(partitionID)
	if dp == nil {
		return
	}
	d := dp.Disk()
	if d == nil {
		return
	}
	if !IsDiskErr(err.Error()) {
		return
	}
	if flag == WriteFlag {
		d.incWriteErrCnt()
	} else if flag == ReadFlag {
		d.incReadErrCnt()
	}
}

func IsDiskErr(errMsg string) bool {
	if strings.Contains(errMsg, syscall.EIO.Error()) || strings.Contains(errMsg, syscall.EROFS.Error()) {
		return true
	}

	return false
}
