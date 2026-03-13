package main

import (
	"fmt"

	"github.com/slidebolt/sdk-types"
)

type addressLookup interface {
	FindEntity(pluginID, deviceID, entityID string) (types.Entity, error)
}

type gatewayAddressLookup struct{}

func (gatewayAddressLookup) FindEntity(pluginID, deviceID, entityID string) (types.Entity, error) {
	return findEntity(pluginID, deviceID, entityID)
}

type Resolver struct {
	lookup addressLookup
}

func AddressResolver() *Resolver {
	return &Resolver{lookup: gatewayAddressLookup{}}
}

func (r *Resolver) ResolveEntityType(pluginID, deviceID, entityID string) (string, error) {
	ent, err := r.lookup.FindEntity(pluginID, deviceID, entityID)
	if err != nil {
		return "", commandErr(CommandErrNotFound, "entity not found", errCommandTargetNotFound)
	}
	return ent.Domain, nil
}

// ResolveEntity returns the full entity and validates the requested action is
// supported. Action validation is skipped for group entities (CommandQuery != nil)
// since validation happens at the leaf level during fan-out.
func (r *Resolver) ResolveEntity(pluginID, deviceID, entityID, action string) (types.Entity, error) {
	ent, err := r.lookup.FindEntity(pluginID, deviceID, entityID)
	if err != nil {
		return types.Entity{}, commandErr(CommandErrNotFound, "entity not found", errCommandTargetNotFound)
	}
	if ent.CommandQuery == nil && len(ent.Actions) > 0 && !containsAction(ent.Actions, action) {
		return types.Entity{}, commandErr(CommandErrUnsupportedAction,
			fmt.Sprintf("action %q is not supported by entity %q; supported: %v", action, entityID, ent.Actions),
			nil)
	}
	return ent, nil
}
