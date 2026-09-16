package claim

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/yondaime-kun/zcode-proxy-go/internal/auth"
	"github.com/yondaime-kun/zcode-proxy-go/internal/config"
	"github.com/yondaime-kun/zcode-proxy-go/internal/proxy"
)

type Scheduler struct {
	cfg       *config.Config
	account   string
	cred      *auth.Credential
	stopChan  chan struct{}
	holdUntil time.Time
	onSuccess func(account, planID string)
}

func (s *Scheduler) AccountTag() string {
	name := s.account
	if name == "" {
		name = "default"
	}
	if s.cred != nil && s.cred.UserId != "" {
		uid := s.cred.UserId
		if len(uid) > 8 {
			uid = uid[:8] + "…"
		}
		return fmt.Sprintf("[%s · %s]", name, uid)
	}
	return fmt.Sprintf("[%s]", name)
}

func StartScheduler(cfg *config.Config, account string, cred *auth.Credential, onSuccess ...func(account, planID string)) *Scheduler {
	var cb func(account, planID string)
	if len(onSuccess) > 0 {
		cb = onSuccess[0]
	}
	s := &Scheduler{
		cfg:       cfg,
		account:   account,
		cred:      cred,
		stopChan:  make(chan struct{}),
		onSuccess: cb,
	}
	go s.run()
	return s
}

func (s *Scheduler) Stop() {
	close(s.stopChan)
}

func (s *Scheduler) run() {
	interval := time.Duration(s.cfg.Claim.PollIntervalMs) * time.Millisecond
	if interval < 10*time.Second {
		interval = 5 * time.Minute
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Initial check shortly after startup
	time.Sleep(3 * time.Second)
	s.tick()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

func (s *Scheduler) tick() {
	if time.Now().Before(s.holdUntil) {
		return
	}

	jwt := s.cred.Jwt
	if jwt == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := NewClient(s.cfg.Claim.Origin, jwt, s.cfg)
	plans, err := client.GetPreviews(ctx)
	if err != nil {
		return
	}

	if len(plans) == 0 {
		return
	}

	// Find target plan
	var target *ClaimablePlan
	wanted := s.cfg.Claim.PlanId
	if wanted != "" {
		for i := range plans {
			if plans[i].PlanID == wanted {
				target = &plans[i]
				break
			}
		}
	}
	if target == nil {
		target = &plans[0]
		for i := 1; i < len(plans); i++ {
			if plans[i].Priority > target.Priority {
				target = &plans[i]
			}
		}
	}

	tag := s.AccountTag()
	log.Printf("[claim] %s Found active plan: %s (%s, priority %d)", tag, target.PlanID, target.Name, target.Priority)

	token, _ := proxy.SolveCaptchaOnDemand(ctx, s.cfg.Identity.AppVersion)
	outcome, err := client.Claim(ctx, target.PlanID, token.VerifyParam, token.Region)
	if err != nil {
		log.Printf("[claim] %s Claim request failed: %v", tag, err)
		s.holdUntil = time.Now().Add(time.Duration(s.cfg.Claim.CooldownMs) * time.Millisecond)
		return
	}

	if outcome.OK {
		log.Printf("[claim] %s SUCCESS: Successfully claimed %s", tag, outcome.PlanID)
		if s.onSuccess != nil {
			s.onSuccess(s.account, outcome.PlanID)
		}
		if outcome.EndsAt != nil {
			s.holdUntil = time.Unix(*outcome.EndsAt, 0)
			log.Printf("[claim] %s Holding until campaign ends: %s", tag, s.holdUntil.Format(time.RFC3339))
		} else {
			s.holdUntil = time.Now().Add(24 * time.Hour)
		}
	} else {
		log.Printf("[claim] %s Claim failed: %s (code %d: %s)", tag, outcome.FailureKind, outcome.Code, outcome.Message)
		if outcome.FailureEndsAt != nil {
			s.holdUntil = time.Unix(*outcome.FailureEndsAt, 0)
		} else {
			s.holdUntil = time.Now().Add(time.Duration(s.cfg.Claim.CooldownMs) * time.Millisecond)
		}
	}
}

func PrintPlans(plans []ClaimablePlan) {
	fmt.Printf("Claimable plans (%d):\n", len(plans))
	for _, p := range plans {
		var window string
		if p.StartsAt != nil && p.EndsAt != nil {
			window = fmt.Sprintf(" %s -> %s", time.Unix(*p.StartsAt, 0).Format(time.RFC3339), time.Unix(*p.EndsAt, 0).Format(time.RFC3339))
		}
		fmt.Printf("  - %s  \"%s\"  priority=%d%s\n", p.PlanID, p.Name, p.Priority, window)
		for _, e := range p.Entitlements {
			quota := ""
			if e.GrantUnits > 0 {
				quota = fmt.Sprintf(" %d %s", e.GrantUnits, e.UnitType)
			}
			name := e.ShowName
			if name == "" {
				name = e.EntitlementID
			}
			fmt.Printf("      * %s%s\n", name, quota)
		}
	}
}
