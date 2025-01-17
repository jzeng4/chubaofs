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
	"fmt"
	"io/ioutil"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/chubaofs/chubaofs/proto"
	"github.com/chubaofs/chubaofs/util/exporter"
	"github.com/chubaofs/chubaofs/util/log"
	"os"
)

var (
	// RegexpDataPartitionDir validates the directory name of a data partition.
	RegexpDataPartitionDir, _ = regexp.Compile("^datapartition_(\\d)+_(\\d)+$")
)

// Disk represents the structure of the disk
type Disk struct {
	sync.RWMutex
	Path        string
	ReadErrCnt  uint64 // number of read errors
	WriteErrCnt uint64 // number of write errors

	Total       uint64
	Used        uint64
	Available   uint64
	Unallocated uint64
	Allocated   uint64

	MaxErrCnt     int // maximum number of errors
	Status        int // disk status such as READONLY
	ReservedSpace uint64

	RejectWrite  bool
	partitionMap map[uint64]*DataPartition
	space        *SpaceManager
}

type PartitionVisitor func(dp *DataPartition)

func NewDisk(path string, maxErrCnt int, space *SpaceManager) (d *Disk) {
	d = new(Disk)
	d.Path = path
	d.MaxErrCnt = maxErrCnt
	d.RejectWrite = false
	d.space = space
	d.partitionMap = make(map[uint64]*DataPartition)
	d.computeUsage()
	d.updateSpaceInfo()
	d.startScheduleToUpdateSpaceInfo()
	return
}

// PartitionCount returns the number of partitions in the partition map.
func (d *Disk) PartitionCount() int {
	d.RLock()
	defer d.RUnlock()
	return len(d.partitionMap)
}

const (
	ReservedPersent = 0.02
)

// Compute the disk usage
func (d *Disk) computeUsage() (err error) {
	d.RLock()
	defer d.RUnlock()
	fs := syscall.Statfs_t{}
	err = syscall.Statfs(d.Path, &fs)
	if err != nil {
		return
	}
	d.ReservedSpace = uint64(float64(fs.Blocks*uint64(fs.Bsize)) * 0.02)
	if d.ReservedSpace > DefaultDiskRetainMax {
		d.ReservedSpace = DefaultDiskRetainMax
	}
	if d.ReservedSpace < DefaultDiskRetainMin {
		d.ReservedSpace = DefaultDiskRetainMin
	}
	total := int64(fs.Blocks*uint64(fs.Bsize) - d.ReservedSpace)
	if total < 0 {
		total = 0
	}
	d.Total = uint64(total)
	available := int64(fs.Bavail*uint64(fs.Bsize) - d.ReservedSpace)
	if available < 0 {
		available = 0
	}
	d.Available = uint64(available)

	//  used := math.Max(0, int64(total - available))
	used := int64(total - available)
	if used < 0 {
		used = 0
	}
	d.Used = uint64(used)

	allocatedSize := int64(0)
	for _, dp := range d.partitionMap {
		allocatedSize += int64(dp.Size())
	}
	atomic.StoreUint64(&d.Allocated, uint64(allocatedSize))
	//  unallocated = math.Max(0, total - allocatedSize)
	unallocated := total - allocatedSize
	if unallocated < 0 {
		unallocated = 0
	}
	if d.Available <= 0 {
		d.RejectWrite = true
	} else {
		d.RejectWrite = false
	}
	d.Unallocated = uint64(unallocated)

	log.LogDebugf("action[computeUsage] disk(%v) all(%v) available(%v) used(%v)", d.Path, d.Total, d.Available, d.Used)

	return
}

func (d *Disk) incReadErrCnt() {
	atomic.AddUint64(&d.ReadErrCnt, 1)
}

func (d *Disk) incWriteErrCnt() {
	atomic.AddUint64(&d.WriteErrCnt, 1)
}

func (d *Disk) startScheduleToUpdateSpaceInfo() {
	go func() {
		updateSpaceInfoTicker := time.NewTicker(5 * time.Second)
		checkStatusTickser := time.NewTicker(time.Minute * 2)
		defer func() {
			updateSpaceInfoTicker.Stop()
			checkStatusTickser.Stop()
		}()
		for {
			select {
			case <-updateSpaceInfoTicker.C:
				d.computeUsage()
				d.updateSpaceInfo()
			case <-checkStatusTickser.C:
				d.checkDiskStatus()
			}
		}
	}()
}

func (d *Disk) autoComputeExtentCrc() {
	for {
		partitions := make([]*DataPartition, 0)
		d.RLock()
		for _, dp := range d.partitionMap {
			partitions = append(partitions, dp)
		}
		d.RUnlock()
		for _, dp := range partitions {
			dp.extentStore.AutoComputeExtentCrc()
		}
		time.Sleep(time.Minute)
	}
}

const (
	DiskStatusFile = ".diskStatus"
)

func (d *Disk) checkDiskStatus() {
	path := path.Join(d.Path, DiskStatusFile)
	fp, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0755)
	if err != nil {
		d.triggerDiskError(err)
		return
	}
	defer fp.Close()
	data := []byte(DiskStatusFile)
	_, err = fp.WriteAt(data, 0)
	if err != nil {
		d.triggerDiskError(err)
		return
	}
	if err = fp.Sync(); err != nil {
		d.triggerDiskError(err)
		return
	}
	if _, err = fp.ReadAt(data, 0); err != nil {
		d.triggerDiskError(err)
		return
	}
}

func (d *Disk) triggerDiskError(err error) {
	if err == nil {
		return
	}
	if IsDiskErr(err.Error()) {
		mesg := fmt.Sprintf("disk path %v error on %v", d.Path, LocalIP)
		exporter.Warning(mesg)
		log.LogErrorf(mesg)
		d.ForceExitRaftStore()
		d.Status = proto.Unavailable
	}
	return
}

func (d *Disk) updateSpaceInfo() (err error) {
	var statsInfo syscall.Statfs_t
	if err = syscall.Statfs(d.Path, &statsInfo); err != nil {
		d.incReadErrCnt()
	}
	if d.Status == proto.Unavailable {
		mesg := fmt.Sprintf("disk path %v error on %v", d.Path, LocalIP)
		log.LogErrorf(mesg)
		exporter.Warning(mesg)
		d.ForceExitRaftStore()
	} else if d.Available <= 0 {
		d.Status = proto.ReadOnly
	} else {
		d.Status = proto.ReadWrite
	}
	log.LogDebugf("action[updateSpaceInfo] disk(%v) total(%v) available(%v) remain(%v) "+
		"restSize(%v) maxErrs(%v) readErrs(%v) writeErrs(%v) status(%v)", d.Path,
		d.Total, d.Available, d.Unallocated, d.ReservedSpace, d.MaxErrCnt, d.ReadErrCnt, d.WriteErrCnt, d.Status)
	return
}

// AttachDataPartition adds a data partition to the partition map.
func (d *Disk) AttachDataPartition(dp *DataPartition) {
	d.Lock()
	d.partitionMap[dp.partitionID] = dp
	d.Unlock()

	d.computeUsage()
}

// DetachDataPartition removes a data partition from the partition map.
func (d *Disk) DetachDataPartition(dp *DataPartition) {
	d.Lock()
	delete(d.partitionMap, dp.partitionID)
	d.Unlock()

	d.computeUsage()
}

// GetDataPartition returns the data partition based on the given partition ID.
func (d *Disk) GetDataPartition(partitionID uint64) (partition *DataPartition) {
	d.RLock()
	defer d.RUnlock()
	return d.partitionMap[partitionID]
}

func (d *Disk) ForceExitRaftStore() {
	partitionList := d.DataPartitionList()
	for _, partitionID := range partitionList {
		partition := d.GetDataPartition(partitionID)
		partition.partitionStatus = proto.Unavailable
		partition.stopRaft()
	}
}

// DataPartitionList returns a list of the data partitions
func (d *Disk) DataPartitionList() (partitionIDs []uint64) {
	d.Lock()
	defer d.Unlock()
	partitionIDs = make([]uint64, 0, len(d.partitionMap))
	for _, dp := range d.partitionMap {
		partitionIDs = append(partitionIDs, dp.partitionID)
	}
	return
}

func unmarshalPartitionName(name string) (partitionID uint32, partitionSize int, err error) {
	arr := strings.Split(name, "_")
	if len(arr) != 3 {
		err = fmt.Errorf("error DataPartition name(%v)", name)
		return
	}
	var (
		pID int
	)
	if pID, err = strconv.Atoi(arr[1]); err != nil {
		return
	}
	if partitionSize, err = strconv.Atoi(arr[2]); err != nil {
		return
	}
	partitionID = uint32(pID)
	return
}

func (d *Disk) isPartitionDir(filename string) (isPartitionDir bool) {
	isPartitionDir = RegexpDataPartitionDir.MatchString(filename)
	return
}

// RestorePartition reads the files stored on the local disk and restores the data partitions.
func (d *Disk) RestorePartition(visitor PartitionVisitor) {
	var (
		partitionID   uint32
		partitionSize int
	)

	fileInfoList, err := ioutil.ReadDir(d.Path)
	if err != nil {
		log.LogErrorf("action[RestorePartition] read dir(%v) err(%v).", d.Path, err)
		return
	}

	var wg sync.WaitGroup
	for _, fileInfo := range fileInfoList {
		filename := fileInfo.Name()
		if !d.isPartitionDir(filename) {
			continue
		}

		if partitionID, partitionSize, err = unmarshalPartitionName(filename); err != nil {
			log.LogErrorf("action[RestorePartition] unmarshal partitionName(%v) from disk(%v) err(%v) ",
				filename, d.Path, err.Error())
			continue
		}
		log.LogDebugf("acton[RestorePartition] disk(%v) path(%v) PartitionID(%v) partitionSize(%v).",
			d.Path, fileInfo.Name(), partitionID, partitionSize)
		wg.Add(1)

		go func(partitionID uint32, filename string) {
			var (
				dp  *DataPartition
				err error
			)
			defer wg.Done()
			if dp, err = LoadDataPartition(path.Join(d.Path, filename), d); err != nil {
				mesg := fmt.Sprintf("action[RestorePartition] new partition(%v) err(%v) ",
					partitionID, err.Error())
				log.LogError(mesg)
				exporter.Warning(mesg)
				return
			}
			if visitor != nil {
				visitor(dp)
			}

		}(partitionID, filename)
	}
	wg.Wait()
}

func (d *Disk) AddSize(size uint64) {
	atomic.AddUint64(&d.Allocated, size)
}

func (d *Disk) getSelectWeight() float64 {
	return float64(atomic.LoadUint64(&d.Allocated)) / float64(d.Total)
}
