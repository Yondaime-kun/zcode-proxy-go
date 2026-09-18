package proxy

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"time"
)

type CaptchaTokenEntry struct {
	Token     *CaptchaToken
	CreatedAt time.Time
}

type CaptchaPool struct {
	mu           sync.Mutex
	tokens       []*CaptchaTokenEntry
	minSize      int
	maxSize      int
	tokenTTL     time.Duration
	idleTimeout  time.Duration
	appVersion   string

	isSolving    bool
	solveDone    chan struct{}
	lastTakeAt   time.Time
	stopCh       chan struct{}
	refillWakeCh chan struct{}
	running      bool
}

func NewCaptchaPool(appVersion string) *CaptchaPool {
	minSize := 2
	if v := os.Getenv("CAPTCHA_POOL_MIN"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			minSize = n
		}
	}
	maxSize := 5
	if v := os.Getenv("CAPTCHA_POOL_MAX"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= minSize {
			maxSize = n
		}
	}

	return &CaptchaPool{
		minSize:      minSize,
		maxSize:      maxSize,
		tokenTTL:     75 * time.Second, // Tokens valid for ~95s; 75s gives 20s margin
		idleTimeout:  5 * time.Minute,
		appVersion:   appVersion,
		stopCh:       make(chan struct{}),
		refillWakeCh: make(chan struct{}, 1),
		lastTakeAt:   time.Now(),
	}
}

func (p *CaptchaPool) Start() {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return
	}
	p.running = true
	p.lastTakeAt = time.Now()
	p.mu.Unlock()

	go p.refillLoop()
	p.triggerRefill()
}

func (p *CaptchaPool) Stop() {
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return
	}
	p.running = false
	close(p.stopCh)
	p.mu.Unlock()
}

func (p *CaptchaPool) pruneExpiredLocked(now time.Time) {
	valid := p.tokens[:0]
	for _, entry := range p.tokens {
		if now.Sub(entry.CreatedAt) < p.tokenTTL {
			valid = append(valid, entry)
		}
	}
	p.tokens = valid
}

func (p *CaptchaPool) triggerRefill() {
	select {
	case p.refillWakeCh <- struct{}{}:
	default:
	}
}

// TakeToken returns an unexpired captcha token immediately from the pool (<1ms).
// If the pool is currently empty, it coordinates with the background solver via singleflight
// so multiple concurrent requests do not launch redundant solver processes.
func (p *CaptchaPool) TakeToken(ctx context.Context) (*CaptchaToken, error) {
	for {
		p.mu.Lock()
		p.lastTakeAt = time.Now()
		p.pruneExpiredLocked(p.lastTakeAt)

		// 1. Hot path: token ready in pool (sub-millisecond)
		if len(p.tokens) > 0 {
			entry := p.tokens[0]
			p.tokens = p.tokens[1:]
			p.mu.Unlock()
			p.triggerRefill()
			return entry.Token, nil
		}

		// 2. If already solving, wait for completion rather than spawning another solver
		if p.isSolving {
			done := p.solveDone
			p.mu.Unlock()

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-p.stopCh:
				return nil, fmt.Errorf("captcha pool stopped")
			case <-done:
				// Solver completed, retry loop to grab newly minted token
				continue
			}
		}

		// 3. Pool empty and no solve in progress: initiate solve under lock
		p.isSolving = true
		done := make(chan struct{})
		p.solveDone = done
		p.mu.Unlock()

		tok, err := SolveCaptchaOnDemand(ctx, p.appVersion)

		p.mu.Lock()
		p.isSolving = false
		p.solveDone = nil
		close(done)

		if err != nil {
			p.mu.Unlock()
			return nil, err
		}

		p.mu.Unlock()
		p.triggerRefill()
		return tok, nil
	}
}

// TryTakeToken attempts to grab an already-minted token without blocking.
// Returns nil if no token is available immediately.
func (p *CaptchaPool) TryTakeToken() *CaptchaToken {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	p.pruneExpiredLocked(now)

	if len(p.tokens) > 0 {
		entry := p.tokens[0]
		p.tokens = p.tokens[1:]
		p.lastTakeAt = now
		p.triggerRefill()
		return entry.Token
	}

	p.triggerRefill()
	return nil
}

func (p *CaptchaPool) refillLoop() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-p.refillWakeCh:
		case <-ticker.C:
		}

		p.mu.Lock()
		if !p.running {
			p.mu.Unlock()
			return
		}

		now := time.Now()
		p.pruneExpiredLocked(now)

		// Idle decay: If no request has requested a token for idleTimeout, pause solves
		if now.Sub(p.lastTakeAt) > p.idleTimeout {
			p.mu.Unlock()
			continue
		}

		// Check if we need more tokens
		if len(p.tokens) >= p.minSize || p.isSolving {
			p.mu.Unlock()
			continue
		}

		// Start single background solve
		p.isSolving = true
		done := make(chan struct{})
		p.solveDone = done
		p.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		tok, err := SolveCaptchaOnDemand(ctx, p.appVersion)
		cancel()

		p.mu.Lock()
		p.isSolving = false
		p.solveDone = nil
		if err == nil && tok != nil && tok.VerifyParam != "" {
			if len(p.tokens) < p.maxSize {
				p.tokens = append(p.tokens, &CaptchaTokenEntry{
					Token:     tok,
					CreatedAt: time.Now(),
				})
				log.Printf("[captcha-pool] pre-warmed token ready (pool: %d/%d, region: %s)", len(p.tokens), p.minSize, tok.Region)
			}
		} else {
			log.Printf("[captcha-pool] background mint note: %v", err)
		}
		close(done)

		// If still under minSize, trigger another refill pass
		if len(p.tokens) < p.minSize {
			p.triggerRefill()
		}
		p.mu.Unlock()
	}
}

func (p *CaptchaPool) Stats() (ready int, min int, max int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pruneExpiredLocked(time.Now())
	return len(p.tokens), p.minSize, p.maxSize
}
