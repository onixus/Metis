package analytics_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onixus/metis/internal/analytics"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

func TestDA05_AD02_AdminCanManagePrivateDashboard(t *testing.T) {
	for _, action := range []string{"update", "delete"} {
		t.Run(action, func(t *testing.T) {
			svc := service()
			ctx := context.Background()
			pid := kernel.NewID()
			d, err := svc.Save(ctx, pmScope("owner", pid), analytics.Input{ProductID: pid, Name: "private", Panels: panels()})
			if err != nil {
				t.Fatal(err)
			}
			admin := authz.New(authz.Params{Subject: "admin", Roles: []authz.Role{authz.RoleAdmin}, AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal})
			if _, readErr := svc.Get(ctx, admin, d.ID); !errors.Is(readErr, kernel.ErrForbidden) {
				t.Fatalf("ordinary private read changed: %v", readErr)
			}
			if action == "update" {
				_, err = svc.Save(ctx, admin, analytics.Input{ID: d.ID, ProductID: pid, Name: "updated", Panels: panels()})
			} else {
				err = svc.Delete(ctx, admin, d.ID)
			}
			if err != nil {
				t.Fatalf("admin %s: %v", action, err)
			}
		})
	}
}

func TestDA05_NFS01_ManagementReadRejectsOtherOwnerAndForeignAdmin(t *testing.T) {
	ctx := context.Background()
	store := analytics.NewMemStore()
	product := kernel.NewID()
	own := pmScope("owner", product)
	d := analytics.Dashboard{ID: kernel.NewID(), ProductID: product, Owner: "owner", Name: "private"}
	if err := store.Save(ctx, own, d); err != nil {
		t.Fatal(err)
	}
	for _, sc := range []authz.Scope{{}, pmScope("other", product), authz.New(authz.Params{Subject: "limited-admin", Roles: []authz.Role{authz.RoleAdmin}, Products: map[kernel.ID]authz.Access{kernel.NewID(): authz.AccessPrivate}})} {
		if _, err := store.GetForWrite(ctx, sc, d.ID); !errors.Is(err, kernel.ErrForbidden) {
			t.Fatalf("management read: %v", err)
		}
		if err := store.Delete(ctx, sc, d.ID); !errors.Is(err, kernel.ErrForbidden) {
			t.Fatalf("delete: %v", err)
		}
	}
	if _, err := store.Get(ctx, own, d.ID); err != nil {
		t.Fatal(err)
	}
}
