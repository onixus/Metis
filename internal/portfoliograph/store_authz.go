package portfoliograph

import (
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// RequireSnapshot permits the complete private graph only to trusted portfolio operations.
func RequireSnapshot(sc authz.Scope) error {
	if !sc.Valid() || !sc.SeesAllProducts() || sc.Product(kernel.NilID) < authz.AccessPrivate {
		return kernel.ErrForbidden
	}
	if !sc.HasRole(authz.RoleService) && !sc.HasRole(authz.RoleAdmin) && !sc.HasRole(authz.RoleCPO) {
		return kernel.ErrForbidden
	}
	return nil
}

// RequireProductWrite checks both the action and the explicit product boundary.
func RequireProductWrite(sc authz.Scope, id kernel.ID) error {
	if id == kernel.NilID || sc.Product(id) < authz.AccessPrivate {
		return kernel.ErrForbidden
	}
	return sc.Require(authz.ActionWriteGraph, id)
}

// RequireLinkWrite preserves one writable endpoint plus visibility of the other endpoint.
func RequireLinkWrite(sc authz.Scope, a, b kernel.ID) error {
	if a == kernel.NilID || b == kernel.NilID {
		return kernel.ErrForbidden
	}
	if !sc.Allows(authz.ActionReadStrategic, a) || !sc.Allows(authz.ActionReadStrategic, b) {
		return kernel.ErrForbidden
	}
	if RequireProductWrite(sc, a) != nil && RequireProductWrite(sc, b) != nil {
		return kernel.ErrForbidden
	}
	return nil
}
