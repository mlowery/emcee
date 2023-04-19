package emcee

import (
	"fmt"
	"log"
	"sync"

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

type runner struct {
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

	// this must be a pointer
	wg := &sync.WaitGroup{}
	for w := 0; w < r.threadiness; w++ {
		wg.Add(1)
		go r.worker(r.fn, restConfigCh, errCh, stopCh, wg)
	}

L:
	for _, restConfig := range r.restConfigs {
		select {
		case restConfigCh <- restConfig:
		case <-stopCh:
			break L
		}
	}
	close(restConfigCh)
	log.Println("waiting for all workers to finish")
	wg.Wait()
	close(errCh)
	log.Println("waiting to collect all errors")
	errWG.Wait()
	select {
	case <-stopCh:
		errs = append(errs, fmt.Errorf("interrupted"))
	default:
	}
	log.Println("done")
	return multierr.Combine(errs...)
}

func (r *runner) worker(fn DoInClusterFunc, restConfigCh <-chan *NamedRestConfig, errCh chan<- error, stopCh <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	for restConfig := range restConfigCh {
		select {
		case <-stopCh:
			return
		default:
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
