package ngrok

import (
	"context"
	"fmt"
	"net/url"
	"sync"

	"golang.ngrok.com/ngrok"
	"golang.ngrok.com/ngrok/config"

	"github.com/antimatter-studios/teamagentica/plugins/network-traffic-manager/internal/drivers"
)

const ID = "ngrok"

func init() {
	drivers.Register(ID, New)
}

type driver struct {
	authToken string
	domain    string
	target    string

	mu    sync.RWMutex
	fwd   ngrok.Forwarder
	state string
	url   string
	errs  string
}

// New builds an ngrok driver. Required config: authtoken. Optional: domain.
func New(target string, cfg map[string]string) (drivers.Driver, error) {
	if target == "" {
		return nil, fmt.Errorf("ngrok: target is required")
	}
	token := cfg["authtoken"]
	if token == "" {
		return nil, fmt.Errorf("ngrok: authtoken is required")
	}
	return &driver{
		authToken: token,
		domain:    cfg["domain"],
		target:    target,
		state:     drivers.StateStopped,
	}, nil
}

func (d *driver) Start(ctx context.Context) (drivers.Status, error) {
	d.mu.Lock()
	if d.fwd != nil {
		st := d.statusLocked()
		d.mu.Unlock()
		return st, nil
	}
	d.state = drivers.StateStarting
	d.errs = ""
	d.mu.Unlock()

	targetURL, err := url.Parse("http://" + d.target)
	if err != nil {
		d.setError(fmt.Sprintf("parse target: %v", err))
		return d.Status(), err
	}

	opts := []config.HTTPEndpointOption{}
	if d.domain != "" {
		opts = append(opts, config.WithDomain(d.domain))
	}

	fwd, err := ngrok.ListenAndForward(ctx,
		targetURL,
		config.HTTPEndpoint(opts...),
		ngrok.WithAuthtoken(d.authToken),
	)
	if err != nil {
		d.setError(err.Error())
		return d.Status(), err
	}

	d.mu.Lock()
	d.fwd = fwd
	d.url = fwd.URL()
	d.state = drivers.StateRunning
	st := d.statusLocked()
	d.mu.Unlock()
	return st, nil
}

func (d *driver) Stop(ctx context.Context) error {
	d.mu.Lock()
	fwd := d.fwd
	d.fwd = nil
	d.url = ""
	d.state = drivers.StateStopped
	d.mu.Unlock()
	if fwd == nil {
		return nil
	}
	return fwd.Close()
}

func (d *driver) Status() drivers.Status {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.statusLocked()
}

func (d *driver) statusLocked() drivers.Status {
	return drivers.Status{State: d.state, URL: d.url, Error: d.errs}
}

func (d *driver) setError(msg string) {
	d.mu.Lock()
	d.state = drivers.StateError
	d.errs = msg
	d.mu.Unlock()
}
