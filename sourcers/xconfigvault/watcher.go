package xconfigvault

import (
	"context"
	"sync"
	"time"
)

// SecretChangeEvent is emitted when a secret value changes.
type SecretChangeEvent struct {
	Path     string
	Key      string
	OldValue string
	NewValue string
	Time     time.Time
}

// WatchOptions configures secret watching.
type WatchOptions struct {
	// Paths to watch for changes (format: "mount/path#key" or "path#key")
	Paths []string

	// RefreshInterval overrides the default refresh interval from cache config.
	RefreshInterval time.Duration

	// OnChange callback when any watched secret changes.
	// Called synchronously - long operations should be done in a goroutine.
	OnChange func(event SecretChangeEvent)
}

// secretWatcher watches secrets for changes.
type secretWatcher struct {
	client    *Client
	options   *WatchOptions
	changes   chan SecretChangeEvent
	stopCh    chan struct{}
	stopped   bool
	callbacks []func(SecretChangeEvent)
	wg        sync.WaitGroup
	mu        sync.Mutex
}

// Watch starts watching secrets for changes.
// Returns a channel that receives change events.
// The returned channel is closed when the context is canceled or the client is closed.
func (c *Client) Watch(ctx context.Context, opts *WatchOptions) (<-chan SecretChangeEvent, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil, ErrClientClosed
	}

	if opts == nil {
		opts = &WatchOptions{}
	}

	if opts.RefreshInterval == 0 {
		opts.RefreshInterval = c.config.Cache.RefreshInterval
	}

	w := &secretWatcher{
		client:  c,
		options: opts,
		changes: make(chan SecretChangeEvent, 100),
		stopCh:  make(chan struct{}),
	}

	c.watcher = w

	w.wg.Add(1)
	go w.run(ctx)

	return w.changes, nil
}

// RegisterCallback registers a callback for secret changes on specific paths.
// The callback is called synchronously when a change is detected.
func (c *Client) RegisterCallback(paths []string, callback func(SecretChangeEvent)) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.watcher == nil {
		// Start a watcher if not already running
		ctx := context.Background()
		_, _ = c.Watch(ctx, &WatchOptions{
			Paths: paths,
		})
	}

	c.watcher.mu.Lock()
	c.watcher.callbacks = append(c.watcher.callbacks, callback)

	// Add new paths to watch
	existingPaths := make(map[string]bool)
	for _, p := range c.watcher.options.Paths {
		existingPaths[p] = true
	}
	for _, p := range paths {
		if !existingPaths[p] {
			c.watcher.options.Paths = append(c.watcher.options.Paths, p)
		}
	}
	c.watcher.mu.Unlock()
}

// StopWatching stops watching all secrets.
func (c *Client) StopWatching() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.watcher != nil {
		c.watcher.stop()
		c.watcher = nil
	}
}

// run is the main watch loop.
func (w *secretWatcher) run(ctx context.Context) {
	defer w.wg.Done()
	defer close(w.changes)

	ticker := time.NewTicker(w.options.RefreshInterval)
	defer ticker.Stop()

	// Store initial values
	values := make(map[string]string)
	for _, path := range w.options.Paths {
		value, err := w.client.Get(ctx, path)
		if err == nil {
			values[path] = value
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.checkForChanges(ctx, values)
		}
	}
}

// checkForChanges checks all watched paths for changes.
func (w *secretWatcher) checkForChanges(ctx context.Context, values map[string]string) {
	w.mu.Lock()
	paths := make([]string, len(w.options.Paths))
	copy(paths, w.options.Paths)
	callbacks := make([]func(SecretChangeEvent), len(w.callbacks))
	copy(callbacks, w.callbacks)
	w.mu.Unlock()

	for _, path := range paths {
		// Invalidate cache to get fresh value
		w.client.InvalidateCache(path)

		newValue, err := w.client.Get(ctx, path)
		if err != nil {
			continue
		}

		oldValue, exists := values[path]
		if !exists {
			values[path] = newValue
			continue
		}

		if newValue != oldValue {
			// Parse path to get key
			_, key, _ := parsePath(path)

			event := SecretChangeEvent{
				Path:     path,
				Key:      key,
				OldValue: oldValue,
				NewValue: newValue,
				Time:     time.Now(),
			}

			// Update stored value
			values[path] = newValue

			// Send to channel (non-blocking)
			select {
			case w.changes <- event:
			default:
				// Channel full, skip
			}

			// Call OnChange callback
			if w.options.OnChange != nil {
				w.options.OnChange(event)
			}

			// Call registered callbacks
			for _, cb := range callbacks {
				cb(event)
			}
		}
	}
}

// stop stops the watcher.
func (w *secretWatcher) stop() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.stopped = true
	w.mu.Unlock()

	close(w.stopCh)
	w.wg.Wait()
}
