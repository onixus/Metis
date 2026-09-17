package seed_test

import (
	"context"
	"testing"

	"github.com/onixus/metis/internal/commitments"
	"github.com/onixus/metis/internal/compliance"
	"github.com/onixus/metis/internal/decisions"
	"github.com/onixus/metis/internal/discovery"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/outbox"
	pg "github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/roadmap"
	"github.com/onixus/metis/internal/seed"
)

// TestSeed_Stage2Idempotent — seed этапа 2 создаёт baseline Server, трек SOAR, обязательства,
// гипотезу и решение один раз; повторный вызов ничего не дублирует.
func TestSeed_Stage2Idempotent(t *testing.T) {
	ctx := context.Background()
	clock := kernel.SystemClock{}
	pub := outbox.NewPublisher(outbox.NewMemStore(), clock)
	sc := identityaccess.ServiceScope("seed")
	portfolio := pg.NewService(pg.NewMemStore(), pub, clock)
	if _, err := seed.Security(ctx, portfolio, sc); err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Infrastructure(ctx, portfolio, sc); err != nil {
		t.Fatal(err)
	}
	d := seed.Stage2Deps{
		Portfolio:   portfolio,
		Roadmap:     roadmap.NewService(roadmap.NewMemStore(), pub, clock),
		Compliance:  compliance.NewService(compliance.NewMemStore(), compliance.NewEvidenceMemStore(), portfolio, pub, clock),
		Commitments: commitments.NewService(commitments.NewMemStore(), pub, clock),
		Discovery:   discovery.NewService(discovery.NewMemStore(), pub, clock, discovery.WithFeatures(portfolio)),
		Decisions:   decisions.NewService(decisions.NewMemStore(), pub, clock),
	}
	for i := 0; i < 2; i++ {
		if err := seed.Stage2(ctx, d, sc); err != nil {
			t.Fatalf("прогон %d: %v", i+1, err)
		}
	}
	server, _ := portfolio.ProductIDByKey(ctx, "server")
	soar, _ := portfolio.ProductIDByKey(ctx, "soar")
	edr, _ := portfolio.ProductIDByKey(ctx, "edr")
	bls, err := d.Compliance.Baselines(ctx, sc, server)
	if err != nil || len(bls) != 1 || bls[0].Version != seed.ServerCertifiedVersion {
		t.Fatalf("baseline Server: %v %v", bls, err)
	}
	tracks, _ := d.Compliance.Tracks(ctx, sc, soar)
	if len(tracks) != 1 || tracks[0].Status != compliance.TrackActive {
		t.Fatalf("трек SOAR: %+v", tracks)
	}
	cs, _ := d.Commitments.List(ctx, sc, commitments.Filter{})
	if len(cs) != 2 {
		t.Fatalf("обязательств %d", len(cs))
	}
	hs, _ := d.Discovery.Hypotheses(ctx, sc, edr, discovery.HypothesisFilter{})
	if len(hs) != 1 {
		t.Fatalf("гипотез %d", len(hs))
	}
	ds, _ := d.Decisions.List(ctx, sc, soar, "")
	if len(ds) != 1 || len(ds[0].Links) != 2 {
		t.Fatalf("решений %d", len(ds))
	}
	res, err := compliance.VerifyEvidenceLog(ctx, compliance.NewEvidenceMemStore())
	if err != nil || !res.OK {
		t.Fatalf("verify: %+v %v", res, err)
	}
}
