package allocation

import (
	"io"
	"sync"
	"time"

	nomad "github.com/hashicorp/nomad/api"
	"github.com/sirupsen/logrus"
)

type Client struct {
	NomadClient *nomad.Client
}

const DefaultPollInterval = 10

const StdErr = "stderr"
const StdOut = "stdout"

func (a *Client) SyncAllocations(nodeID *string, currentAllocations *[]nomad.Allocation,
	addedChan chan<- nomad.Allocation, removedChan chan<- nomad.Allocation, errChan chan<- error, mutex *sync.Mutex, pollInterval int, logger *logrus.Logger) {

	ticker := time.NewTicker(time.Second * time.Duration(pollInterval))

	for {
		<- ticker.C

		currentAllocs := *currentAllocations

		var currentAllocIds []string

		for _, alloc := range currentAllocs {
			currentAllocIds = append(currentAllocIds, alloc.ID)
		}

		logger.Infof("Current Allocations: %v", currentAllocIds)

		allocations, err := a.GetAllocationsForNode(nodeID)

		var foundAllocations []nomad.Allocation

		if err != nil {
			errChan <- err
		} else {
			for _, allocation := range allocations {
				if allocation.ClientStatus == "running" || allocation.ClientStatus == "restarting" {
					foundAllocations = append(foundAllocations, *allocation)

					if !allocationInSlice(*allocation, currentAllocs) {
						logger.Infof("[%s] Sending allocation to added channel.", allocation.ID)
						addedChan <- *allocation
						logger.Infof("[%s] Allocation sent to added channel.", allocation.ID)
					}
				}
			}

			if len(currentAllocs) > 0 {
				for _, allocation := range currentAllocs {
					if !allocationInSlice(allocation, foundAllocations) {
						logger.Infof("[%s] Sending allocation to remove channel.", allocation.ID)
						removedChan <- allocation
						logger.Infof("[%s] Allocation sent to remove channel.", allocation.ID)
					}
				}
			}

			mutex.Lock()
			*currentAllocations = foundAllocations
			mutex.Unlock()
		}
	}
}

func (a *Client) GetAllocationsForNode(nodeID *string) ([]*nomad.Allocation, error) {
	allocations, _, err := a.NomadClient.Nodes().Allocations(*nodeID, nil)

	return allocations, err
}

func allocationInSlice(item nomad.Allocation, list []nomad.Allocation) bool {
	for _, i := range list {
		if i.ID == item.ID {
			return true
		}
	}

	return false
}

func (a *Client) GetAllocationInfo(ID string) (*nomad.Allocation, error) {
	alloc, _, err := a.NomadClient.Allocations().Info(ID, nil)

	return alloc, err
}

func (a *Client) GetLog(logType string, alloc *nomad.Allocation, taskName string, offset int64) *nomad.StreamFrame {
	data, _ := a.NomadClient.AllocFS().Logs(alloc, false, taskName, logType, "start", offset, nil, nil)

	content := <-data

	return content
}

func (a *Client) GetLogSize(logType string, alloc *nomad.Allocation, taskName string, offset int64) int64 {
	frames, errors := a.NomadClient.AllocFS().Logs(alloc, false, taskName, logType, "start", offset, nil, nil)

	size := 0

	var err error
	var n int

	n, err = readStreamFrame(frames, errors)

	size = size + n

	for err == nil {
		n, err = readStreamFrame(frames, errors)
		size = size + n
	}

	return int64(size)
}

func (a *Client) StreamLog(logType string, alloc *nomad.Allocation, taskName string, offset int64, stopChan <-chan struct{}) (<-chan *nomad.StreamFrame, <-chan error) {
	stream, errors := a.NomadClient.AllocFS().Logs(alloc, true, taskName, logType, "start", offset, stopChan, nil)

	return stream, errors
}

func (a *Client) StatFile(alloc *nomad.Allocation, path string) (*nomad.AllocFileInfo, error) {
	data, _, err := a.NomadClient.AllocFS().Stat(alloc, path, nil)

	return data, err
}

func (a *Client) StreamFile(alloc *nomad.Allocation, path string, offset int64, stopChan <-chan struct{}) (<-chan *nomad.StreamFrame, <-chan error) {
	stream, errors := a.NomadClient.AllocFS().Stream(alloc, path, "start", offset, stopChan, nil)

	return stream, errors
}

func readStreamFrame(frames <-chan *nomad.StreamFrame, errs <-chan error) (int, error) {
	select {
	case frame, ok := <-frames:
		if !ok {
			return 0, io.EOF
		}

		return len(frame.Data), nil
	case err := <-errs:
		return 0, err
	}
}
