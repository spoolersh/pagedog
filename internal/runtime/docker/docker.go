// Package docker runs workers as containers through the Docker Engine API.
package docker

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/spoolersh/pagedog/internal/runtime"
)

const (
	// DefaultNamePrefix starts every container name.
	DefaultNamePrefix = "pagedog-worker-"

	// DefaultStopTimeout is how long a container has to exit on its own before
	// it is killed.
	DefaultStopTimeout = 10 * time.Second
)

// Label marks the containers this package runs, so that a listing finds them
// and nothing else.
const Label = "sh.spooler.pagedog.worker"

var _ runtime.Runtime = (*Runtime)(nil)

// Runtime launches one container per worker. The image is expected to take
// one message and exit.
type Runtime struct {
	Trace Trace

	// Client is the Docker engine client.
	//
	// Zero means one from the environment.
	Client *client.Client

	// Image is the worker image.
	// Required.
	Image string

	// Args are the arguments the image's entrypoint gets.
	Args []string

	// Env is the environment every worker gets, and all of it.
	Env map[string]string

	// Network is the network a worker joins, such as a compose project's.
	//
	// Zero means the engine's default.
	Network string

	// Binds are volumes to mount, as "volume-or-path:/container/path".
	Binds []string

	// NamePrefix starts every container name; a unique suffix follows.
	//
	// Zero means [DefaultNamePrefix].
	NamePrefix string

	// StopTimeout is how long a shutdown waits for a worker to exit before
	// killing it.
	//
	// Zero means [DefaultStopTimeout].
	StopTimeout time.Duration

	once    sync.Once
	initErr error
	client  *client.Client
}

func (r *Runtime) init() error {
	r.once.Do(func() {
		if r.Image == "" {
			r.initErr = errors.New("docker: no image")
			return
		}
		r.client = r.Client
		if r.client == nil {
			r.client, r.initErr = client.New(client.FromEnv)
		}
	})
	return r.initErr
}

// Stats lists the running containers this runtime launched.
func (r *Runtime) Stats(ctx context.Context) (runtime.Stats, error) {
	if err := r.init(); err != nil {
		return runtime.Stats{}, err
	}
	list, err := r.list(ctx)
	if err != nil {
		return runtime.Stats{}, err
	}
	stats := runtime.Stats{
		Workers: make([]runtime.Worker, 0, len(list)),
	}
	for _, c := range list {
		stats.Workers = append(stats.Workers, runtime.Worker{
			ID:   c.ID,
			Name: containerName(c.Names),
		})
	}
	return stats, nil
}

// containerName is the name the engine lists first, without the leading
// slash it adds.
func containerName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return strings.TrimPrefix(names[0], "/")
}

// Launch creates and starts one container.
func (r *Runtime) Launch(ctx context.Context) (runtime.Worker, error) {
	if err := r.init(); err != nil {
		return runtime.Worker{}, err
	}
	var (
		suffix = strconv.FormatInt(time.Now().UnixNano(), 36)
		name   = cmp.Or(r.NamePrefix, DefaultNamePrefix) + suffix
	)
	id, err := r.create(ctx, name)
	if err != nil {
		return runtime.Worker{}, err
	}
	if err := r.start(ctx, id); err != nil {
		// A created container that never started is of no use; remove it so
		// that it does not linger. Its error is the start's.
		_ = r.remove(ctx, id)
		return runtime.Worker{}, err
	}
	return runtime.Worker{
		ID:   id,
		Name: name,
	}, nil
}

// Shutdown stops the container, waiting [Runtime.StopTimeout] for it to exit
// on its own; the engine removes it once it has.
func (r *Runtime) Shutdown(ctx context.Context, w runtime.Worker) error {
	if err := r.init(); err != nil {
		return err
	}
	err := r.stop(ctx, w.ID)
	if errdefs.IsNotFound(err) {
		// Exited on its own already, and the engine removed it.
		return nil
	}
	return err
}

func (r *Runtime) list(ctx context.Context) (items []container.Summary, err error) {
	done := r.Trace.onList()
	defer func() { done(len(items), err) }()

	res, err := r.client.ContainerList(ctx, client.ContainerListOptions{
		Filters: make(client.Filters).
			Add("label", Label).
			Add("status", string(container.StateRunning)),
	})
	if err != nil {
		return nil, err
	}
	return res.Items, nil
}

func (r *Runtime) create(ctx context.Context, name string) (id string, err error) {
	done := r.Trace.onCreate(name)
	defer func() { done(id, err) }()

	env := make([]string, 0, len(r.Env))
	for _, k := range slices.Sorted(maps.Keys(r.Env)) {
		env = append(env, k+"="+r.Env[k])
	}
	res, err := r.client.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: name,
		Config: &container.Config{
			Image:  r.Image,
			Cmd:    r.Args,
			Env:    env,
			Labels: map[string]string{Label: ""},
		},
		HostConfig: &container.HostConfig{
			NetworkMode: container.NetworkMode(r.Network),
			Binds:       r.Binds,
			// A worker's logs are for a collector to keep: the container
			// goes once it exits.
			AutoRemove: true,
		},
	})
	if err != nil {
		return "", err
	}
	return res.ID, nil
}

func (r *Runtime) start(ctx context.Context, id string) (err error) {
	done := r.Trace.onStart(id)
	defer func() { done(err) }()

	_, err = r.client.ContainerStart(ctx, id, client.ContainerStartOptions{})
	return err
}

func (r *Runtime) stop(ctx context.Context, id string) (err error) {
	done := r.Trace.onStop(id)
	defer func() { done(err) }()

	timeout := int(cmp.Or(r.StopTimeout, DefaultStopTimeout).Seconds())
	_, err = r.client.ContainerStop(ctx, id, client.ContainerStopOptions{
		Timeout: &timeout,
	})
	return err
}

func (r *Runtime) remove(ctx context.Context, id string) (err error) {
	done := r.Trace.onRemove(id)
	defer func() { done(err) }()

	_, err = r.client.ContainerRemove(ctx, id, client.ContainerRemoveOptions{
		Force: true,
	})
	if errdefs.IsNotFound(err) || errdefs.IsConflict(err) {
		// Removed already, or being removed: by AutoRemove or by hand.
		return nil
	}
	return err
}
