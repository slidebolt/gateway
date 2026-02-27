package main

// Blank imports trigger the init() in each entity package,
// which registers their DomainDescriptor into the types registry.
import (
	_ "github.com/slidebolt/sdk-entities/light"
	_ "github.com/slidebolt/sdk-entities/switch"
)
