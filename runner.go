package emcee

import (
	"fmt"
	"sync"
	"sync/atomic"

	"go.uber.org/multierr"
	"k8s.io/client-go/rest"
)

type DoInClusterFunc func(*NamedRestConfig) error

type NamedRestConfig struct {
	ConfigName string
	rest.Config
}

func NewRunner(threadiness int, restConfigs []*NamedRestConfig, fn DoInClusterFunc) *runner {
	return &runner{
		threadiness: threadiness,
		fn:          fn,
		restConfigs: restConfigs,
	}
}

type AtomicBool struct {
	flag int32
}

func (b *AtomicBool) Set() {
	var i int32 = 1
	atomic.StoreInt32(&(b.flag), int32(i))
}

func (b *AtomicBool) Get() bool {
	if atomic.LoadInt32(&(b.flag)) != 0 {
		return true
	}
	return false
}

type runner struct {
	interrupted AtomicBool
	fn          DoInClusterFunc
	threadiness int
	restConfigs []*NamedRestConfig
}

func (r *runner) Run(stopCh <-chan struct{}) error {
	// unbuffered so that we block when all workers are busy
	restConfigCh := make(chan *NamedRestConfig)
	errCh := make(chan error)

	var errs []error
	// this must be a pointer
	errWG := &sync.WaitGroup{}
	errWG.Add(1)
	go func() {
		defer errWG.Done()
		for err := range errCh {
			if err != nil {
				errs = append(errs, err)
			}
		}
	}()

	go func() {
		<-stopCh
		r.interrupted.Set()
	}()

	// this must be a pointer
	wg := &sync.WaitGroup{}
	for w := 0; w < r.threadiness; w++ {
		wg.Add(1)
		go r.worker(r.fn, restConfigCh, errCh, wg)
	}

	for _, restConfig := range r.restConfigs {
		restConfigCh <- restConfig
	}
	close(restConfigCh)
	wg.Wait()
	close(errCh)
	errWG.Wait()
	if r.interrupted.Get() {
		errs = append(errs, fmt.Errorf("interrupted"))
	}
	return multierr.Combine(errs...)
}

func (r *runner) worker(fn DoInClusterFunc, restConfigCh <-chan *NamedRestConfig, errCh chan<- error, wg *sync.WaitGroup) {
	defer wg.Done()
	for restConfig := range restConfigCh {
		if r.interrupted.Get() {
			return
		}
		err := fn(restConfig)
		if err != nil {
			// wrap it with restConfig for extra context
			err = newRunnerError(restConfig.ConfigName, err)
		}
		errCh <- err
	}
}

func newRunnerError(cluster string, cause error) error {
	return fmt.Errorf("[cluster %3s]: %w", cluster, cause)
}
