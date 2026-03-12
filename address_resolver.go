package main

import "github.com/slidebolt/sdk-types"

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
