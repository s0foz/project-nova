// Package server orchestrates loaded models and their runner lifecycles.
//
// A Server owns the set of currently-loaded models, each with its own runner
// instance and a keep-alive timer that unloads it after a period of inactivity.
// The HTTP API layer and the CLI both drive a *Server to perform generation,
// chat, embedding, and model management.
package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/project-nova/nova/internal/env"
	"github.com/project-nova/nova/internal/llm"
	"github.com/project-nova/nova/internal/llmcpp"
	"github.com/project-nova/nova/internal/model"
	"github.com/project-nova/nova/internal/registry"
)

// RunnerFactory builds a fresh llm.Runner for a manifest. The default factory
// returns a stub runner; production code injects a real runner factory.
type RunnerFactory func() llm.Runner

// DefaultFactory returns a runner factory selected by the NOVA_RUNNER env var:
//
//   - "stub"     -> always the echo stub runner (no real inference)
//   - "llamacpp" -> always the llama.cpp subprocess runner (errors on Load if
//     llama-server is not installed)
//   - "auto"     -> llama.cpp if llama-server is discoverable, else stub
//
// "auto" is the default. This lets a stock Nova install run end-to-end on the
// stub runner, while a machine with llama.cpp installed gets real inference
// automatically.
var DefaultFactory RunnerFactory = func() llm.Runner {
	switch env.RunnerName() {
	case "stub":
		return llm.NewStubRunner()
	case "llamacpp":
		return llmcpp.New()
	default: // "auto" and any unrecognised value
		if llmcpp.Available() {
			return llmcpp.New()
		}
		return llm.NewStubRunner()
	}
}

// LoadedModel is a single resident model with its runner and metadata.
type LoadedModel struct {
	Name      registry.Name
	Manifest  *model.Manifest
	Runner    llm.Runner
	Options   llm.Options
	ExpiresAt time.Time
	Created   time.Time
	// SizeVRAM is an optional VRAM footprint reported by the runner.
	SizeVRAM uint64
}

// Server is the central model orchestrator. It is safe for concurrent use.
type Server struct {
	mu       sync.Mutex
	factory  RunnerFactory
	loaded   map[string]*LoadedModel
	stopCh   chan struct{}
	stopped  bool
	sweepers sync.WaitGroup
	now      func() time.Time
}

// New creates a Server using the given runner factory (or DefaultFactory if nil).
func New(factory RunnerFactory) *Server {
	if factory == nil {
		factory = DefaultFactory
	}
	s := &Server{
		factory: factory,
		loaded:  make(map[string]*LoadedModel),
		stopCh:  make(chan struct{}),
		now:     time.Now,
	}
	s.sweepers.Add(1)
	go s.sweepLoop()
	return s
}

// Stop unloads all models and stops background sweepers. It is idempotent.
func (s *Server) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	close(s.stopCh)
	// Snapshot loaded models to close outside the lock.
	loaded := s.loaded
	s.loaded = make(map[string]*LoadedModel)
	s.mu.Unlock()

	for _, lm := range loaded {
		if err := lm.Runner.Close(); err != nil {
			log.Printf("nova: unload %s: %v", lm.Name, err)
		}
	}
	s.sweepers.Wait()
}

// Load ensures a model is resident in memory and returns it. If the model is
// already loaded its keep-alive timer is refreshed.
func (s *Server) Load(ctx context.Context, name registry.Name, opts llm.Options) (*LoadedModel, error) {
	key := name.String()

	s.mu.Lock()
	if lm, ok := s.loaded[key]; ok {
		lm.ExpiresAt = s.now().Add(keepAlive(opts))
		s.mu.Unlock()
		return lm, nil
	}
	s.mu.Unlock()

	// Read manifest outside lock (disk I/O).
	manifest, err := registry.ReadManifest(name)
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", name, err)
	}

	runner := s.factory()
	if err := runner.Load(ctx, manifest, opts); err != nil {
		return nil, fmt.Errorf("load %s: %w", name, err)
	}

	lm := &LoadedModel{
		Name:      name,
		Manifest:  manifest,
		Runner:    runner,
		Options:   opts,
		Created:   s.now(),
		ExpiresAt: s.now().Add(keepAlive(opts)),
	}

	s.mu.Lock()
	// Another goroutine may have loaded the same model concurrently; dedupe.
	if existing, ok := s.loaded[key]; ok {
		s.mu.Unlock()
		_ = runner.Close()
		existing.ExpiresAt = s.now().Add(keepAlive(opts))
		return existing, nil
	}
	s.loaded[key] = lm
	s.mu.Unlock()
	return lm, nil
}

// Get returns an already-loaded model without loading it, or nil.
func (s *Server) Get(name registry.Name) *LoadedModel {
	s.mu.Lock()
	defer s.mu.Unlock()
	if lm, ok := s.loaded[name.String()]; ok {
		lm.ExpiresAt = s.now().Add(keepAlive(lm.Options))
		return lm
	}
	return nil
}

// Unload removes a model from memory immediately.
func (s *Server) Unload(name registry.Name) error {
	s.mu.Lock()
	lm, ok := s.loaded[name.String()]
	if !ok {
		s.mu.Unlock()
		return ErrNotLoaded
	}
	delete(s.loaded, name.String())
	s.mu.Unlock()

	return lm.Runner.Close()
}

// List returns the currently loaded models (for `nova ps` / /api/ps).
func (s *Server) List() []*LoadedModel {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*LoadedModel, 0, len(s.loaded))
	for _, lm := range s.loaded {
		out = append(out, lm)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name.String() < out[j].Name.String() })
	return out
}

// Generate loads (if needed) and generates a completion for a prompt.
func (s *Server) Generate(ctx context.Context, name registry.Name, prompt string, images []string, opts llm.Options, fn func(llm.Token) error) error {
	lm, err := s.acquire(ctx, name, opts)
	if err != nil {
		return err
	}
	return lm.Runner.Generate(ctx, prompt, images, opts, fn)
}

// Chat loads (if needed) and generates a chat completion.
func (s *Server) Chat(ctx context.Context, name registry.Name, messages []llm.Message, opts llm.Options, fn func(llm.Token) error) error {
	lm, err := s.acquire(ctx, name, opts)
	if err != nil {
		return err
	}
	return lm.Runner.Chat(ctx, messages, opts, fn)
}

// Embed loads (if needed) and returns embeddings.
func (s *Server) Embed(ctx context.Context, name registry.Name, inputs []string, opts llm.Options) ([][]float32, error) {
	lm, err := s.acquire(ctx, name, opts)
	if err != nil {
		return nil, err
	}
	return lm.Runner.Embed(ctx, inputs, opts)
}

// acquire returns a LoadedModel for the name, loading if necessary.
func (s *Server) acquire(ctx context.Context, name registry.Name, opts llm.Options) (*LoadedModel, error) {
	if lm := s.Get(name); lm != nil {
		return lm, nil
	}
	return s.Load(ctx, name, opts)
}

// sweepLoop periodically unloads models whose keep-alive has expired.
func (s *Server) sweepLoop() {
	defer s.sweepers.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.sweep()
		}
	}
}

func (s *Server) sweep() {
	now := s.now()
	var toUnload []*LoadedModel
	s.mu.Lock()
	for key, lm := range s.loaded {
		if !lm.ExpiresAt.IsZero() && now.After(lm.ExpiresAt) {
			toUnload = append(toUnload, lm)
			delete(s.loaded, key)
		}
	}
	s.mu.Unlock()
	for _, lm := range toUnload {
		if env.Debug() {
			log.Printf("nova: keep-alive expired, unloading %s", lm.Name)
		}
		_ = lm.Runner.Close()
	}
}

// keepAlive resolves the effective keep-alive duration for an option set.
func keepAlive(opts llm.Options) time.Duration {
	if opts.KeepAlive <= 0 {
		return 5 * time.Minute
	}
	return opts.KeepAlive
}

// ErrNotLoaded is returned when an operation references a model that is not
// resident and could not be loaded.
var ErrNotLoaded = errors.New("model not loaded")
