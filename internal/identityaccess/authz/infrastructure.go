package authz

import "github.com/onixus/metis/internal/kernel"

// RequireInfrastructure protects complete hash chains and worker queues. These
// protocols cannot be filtered by product without breaking ordering or delivery.
func RequireInfrastructure(sc Scope) error {
	if !sc.Valid() || !sc.HasRole(RoleService) || !sc.SeesAllProducts() || sc.Product(kernel.NilID) < AccessPrivate || sc.Audience() != AudienceInternal {
		return kernel.ErrForbidden
	}
	return nil
}
