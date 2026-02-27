package main

import (
	"encoding/json"
	"os"
)

func loadVirtualStore(dataDir string) *virtualStore {
	vs := &virtualStore{
		entities: make(map[string]virtualEntityRecord),
		commands: make(map[string]virtualCommandRecord),
		events:   make([]observedEvent, 0),
		dataDir:  dataDir,
	}
	_ = os.MkdirAll(dataDir, 0o755)
	if data, err := os.ReadFile(virtualFile(dataDir, "virtual_entities.json")); err == nil {
		var ents map[string]virtualEntityRecord
		if json.Unmarshal(data, &ents) == nil {
			vs.entities = ents
		}
	}
	if data, err := os.ReadFile(virtualFile(dataDir, "virtual_commands.json")); err == nil {
		var cmds map[string]virtualCommandRecord
		if json.Unmarshal(data, &cmds) == nil {
			vs.commands = cmds
		}
	}
	if data, err := os.ReadFile(virtualFile(dataDir, "event_journal.json")); err == nil {
		var journal []observedEvent
		if json.Unmarshal(data, &journal) == nil {
			vs.events = journal
		}
	}
	return vs
}

func (vs *virtualStore) persistLocked() {
	ents, _ := json.MarshalIndent(vs.entities, "", "  ")
	_ = os.WriteFile(virtualFile(vs.dataDir, "virtual_entities.json"), ents, 0o644)
	cmds, _ := json.MarshalIndent(vs.commands, "", "  ")
	_ = os.WriteFile(virtualFile(vs.dataDir, "virtual_commands.json"), cmds, 0o644)
	journal, _ := json.MarshalIndent(vs.events, "", "  ")
	_ = os.WriteFile(virtualFile(vs.dataDir, "event_journal.json"), journal, 0o644)
}

func (vs *virtualStore) appendEventLocked(evt observedEvent) {
	vs.events = append(vs.events, evt)
	if len(vs.events) > 5000 {
		vs.events = vs.events[len(vs.events)-5000:]
	}
}
