package scripting

import (
	"encoding/json"
	"testing"

	lightdomain "github.com/slidebolt/sdk-entities/light"
	lightstripdomain "github.com/slidebolt/sdk-entities/light_strip"
	"github.com/slidebolt/sdk-types"
)

type helperTestSubmitter struct {
	commands []map[string]any
}

func (s *helperTestSubmitter) Submit(pluginID, deviceID, entityID string, payload json.RawMessage) (types.CommandStatus, error) {
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		return types.CommandStatus{}, err
	}
	m["_plugin_id"] = pluginID
	m["_device_id"] = deviceID
	m["_entity_id"] = entityID
	s.commands = append(s.commands, m)
	return types.CommandStatus{CommandID: "cmd-test"}, nil
}

type helperTestFinder struct{}

func (helperTestFinder) FindEntities(q types.SearchQuery) []types.Entity { return nil }
func (helperTestFinder) FindDevices(q types.SearchQuery) []types.Device  { return nil }

type helperTestBus struct{}

func (helperTestBus) Publish(subject string, data []byte) error { return nil }
func (helperTestBus) Subscribe(subject string, handler func([]byte)) (Subscription, error) {
	return helperTestSub{}, nil
}

type helperTestSub struct{}

func (helperTestSub) Unsubscribe() error { return nil }

type helperTestSessionStore struct {
	sessions map[string]map[string]any
}

func newHelperTestSessionStore() *helperTestSessionStore {
	return &helperTestSessionStore{sessions: make(map[string]map[string]any)}
}

func (s *helperTestSessionStore) LoadSession(sessionID string) (map[string]any, bool) {
	payload, ok := s.sessions[sessionID]
	if !ok {
		return nil, false
	}
	out := make(map[string]any, len(payload))
	for k, v := range payload {
		out[k] = v
	}
	return out, true
}

func (s *helperTestSessionStore) SaveSession(sessionID string, payload map[string]any) error {
	out := make(map[string]any, len(payload))
	for k, v := range payload {
		out[k] = v
	}
	s.sessions[sessionID] = out
	return nil
}

func (s *helperTestSessionStore) DeleteSession(sessionID string) error {
	delete(s.sessions, sessionID)
	return nil
}

func TestLuaVM_LightHelpersDispatchCommands(t *testing.T) {
	submitter := &helperTestSubmitter{}
	entity := types.Entity{
		ID:       "light-1",
		PluginID: "plugin-test",
		DeviceID: "dev-1",
		Domain:   lightdomain.Type,
		Actions: []string{
			lightdomain.ActionTurnOn,
			lightdomain.ActionTurnOff,
			lightdomain.ActionSetBrightness,
			lightdomain.ActionSetRGB,
			lightdomain.ActionSetTemperature,
		},
	}
	vm, err := NewLuaVM(entity, `
Light.On()
Light.SetBrightness(42)
Light.SetRGB({1, 2, 3})
Light.SetTemperature(3000)
`, Services{
		Commands: submitter,
		Finder:   helperTestFinder{},
		Bus:      helperTestBus{},
		Timers:   NewOSTimerService(),
	})
	if err != nil {
		t.Fatalf("NewLuaVM: %v", err)
	}
	defer vm.Stop()

	if got := len(submitter.commands); got != 4 {
		t.Fatalf("command count = %d, want 4", got)
	}
	if got := submitter.commands[0]["type"]; got != lightdomain.ActionTurnOn {
		t.Fatalf("cmd[0].type = %#v, want %q", got, lightdomain.ActionTurnOn)
	}
	if got := submitter.commands[1]["brightness"]; got != float64(42) {
		t.Fatalf("cmd[1].brightness = %#v, want 42", got)
	}
	if got := submitter.commands[2]["rgb"]; !equalAnySlice(got, []int{1, 2, 3}) {
		t.Fatalf("cmd[2].rgb = %#v, want [1 2 3]", got)
	}
	if got := submitter.commands[3]["temperature"]; got != float64(3000) {
		t.Fatalf("cmd[3].temperature = %#v, want 3000", got)
	}
}

func TestLuaVM_StripHelpersDispatchCommands(t *testing.T) {
	submitter := &helperTestSubmitter{}
	entity := types.Entity{
		ID:       "strip-1",
		PluginID: "plugin-test",
		DeviceID: "dev-1",
		Domain:   lightstripdomain.Type,
		Meta: map[string]json.RawMessage{
			"strip_members": json.RawMessage(`[
				{"index":0,"plugin_id":"plugin-a","device_id":"device-a","entity_id":"light-a"},
				{"index":1,"plugin_id":"plugin-b","device_id":"device-b","entity_id":"light-b"},
				{"index":2,"plugin_id":"plugin-c","device_id":"device-c","entity_id":"light-c"}
			]`),
		},
	}
	vm, err := NewLuaVM(entity, `
Strip.On()
Strip.Fill({10, 20, 30})
Strip.SetSegment(2, {4, 5, 6})
`, Services{
		Commands: submitter,
		Finder:   helperTestFinder{},
		Bus:      helperTestBus{},
		Timers:   NewOSTimerService(),
	})
	if err != nil {
		t.Fatalf("NewLuaVM: %v", err)
	}
	defer vm.Stop()

	if got := len(submitter.commands); got != 7 {
		t.Fatalf("command count = %d, want 7", got)
	}
	for i := 0; i < 3; i++ {
		if got := submitter.commands[i]["type"]; got != lightdomain.ActionTurnOn {
			t.Fatalf("cmd[%d].type = %#v, want %q", i, got, lightdomain.ActionTurnOn)
		}
	}
	for i := 3; i < 6; i++ {
		if got := submitter.commands[i]["rgb"]; !equalAnySlice(got, []int{10, 20, 30}) {
			t.Fatalf("cmd[%d].rgb = %#v, want [10 20 30]", i, got)
		}
	}
	if got := submitter.commands[6]["type"]; got != lightdomain.ActionSetRGB {
		t.Fatalf("cmd[6].type = %#v, want %q", got, lightdomain.ActionSetRGB)
	}
	if got := submitter.commands[6]["rgb"]; !equalAnySlice(got, []int{4, 5, 6}) {
		t.Fatalf("cmd[6].rgb = %#v, want [4 5 6]", got)
	}
	if got := submitter.commands[6]["_plugin_id"]; got != "plugin-c" {
		t.Fatalf("cmd[6] plugin = %#v, want plugin-c", got)
	}
	if got := submitter.commands[6]["_device_id"]; got != "device-c" {
		t.Fatalf("cmd[6] device = %#v, want device-c", got)
	}
	if got := submitter.commands[6]["_entity_id"]; got != "light-c" {
		t.Fatalf("cmd[6] entity = %#v, want light-c", got)
	}
}

func TestLuaVM_StripLengthReadsStripMembersMeta(t *testing.T) {
	entity := types.Entity{
		ID:       "strip-1",
		PluginID: "plugin-test",
		DeviceID: "dev-1",
		Domain:   lightstripdomain.Type,
		Meta: map[string]json.RawMessage{
			"strip_members": json.RawMessage(`[
				{"index":0,"plugin_id":"a","device_id":"d1","entity_id":"e1"},
				{"index":1,"plugin_id":"a","device_id":"d2","entity_id":"e2"},
				{"index":2,"plugin_id":"a","device_id":"d3","entity_id":"e3"}
			]`),
		},
	}
	vm, err := NewLuaVM(entity, `
LengthValue = Strip.Length()
`, Services{
		Commands: &helperTestSubmitter{},
		Finder:   helperTestFinder{},
		Bus:      helperTestBus{},
		Timers:   NewOSTimerService(),
	})
	if err != nil {
		t.Fatalf("NewLuaVM: %v", err)
	}
	defer vm.Stop()

	got, err := vm.GetGlobalNumber("LengthValue")
	if err != nil {
		t.Fatalf("GetGlobalNumber: %v", err)
	}
	if got != 3 {
		t.Fatalf("LengthValue = %v, want 3", got)
	}
}

func TestLuaVM_DomainHelpersOnlyInjectedForMatchingDomain(t *testing.T) {
	entity := types.Entity{
		ID:       "switch-1",
		PluginID: "plugin-test",
		DeviceID: "dev-1",
		Domain:   "switch",
	}
	vm, err := NewLuaVM(entity, `
if Light ~= nil then
  error("Light helper should not be injected")
end
if Strip ~= nil then
  error("Strip helper should not be injected")
end
`, Services{
		Commands: &helperTestSubmitter{},
		Finder:   helperTestFinder{},
		Bus:      helperTestBus{},
		Timers:   NewOSTimerService(),
	})
	if err != nil {
		t.Fatalf("NewLuaVM: %v", err)
	}
	defer vm.Stop()
}

func TestLuaVM_SessionAPIsPersistPerInstanceInMemory(t *testing.T) {
	sessions := newHelperTestSessionStore()
	entity := types.Entity{
		ID:       "light-1",
		PluginID: "plugin-test",
		DeviceID: "dev-1",
		Domain:   lightdomain.Type,
	}

	vmA, err := NewLuaVMWithInstance(entity, `
local state = This.LoadSession()
local count = 1
if state ~= nil and state.count ~= nil then
  count = state.count + 1
end
This.SaveSession({count = count})
`, ScriptInstance{SessionID: "sess-a", ScriptRef: "Counter"}, Services{
		Commands: &helperTestSubmitter{},
		Finder:   helperTestFinder{},
		Bus:      helperTestBus{},
		Timers:   NewOSTimerService(),
		Sessions: sessions,
	})
	if err != nil {
		t.Fatalf("NewLuaVMWithInstance A: %v", err)
	}
	defer vmA.Stop()

	vmB, err := NewLuaVMWithInstance(entity, `
local state = This.LoadSession()
local count = 1
if state ~= nil and state.count ~= nil then
  count = state.count + 1
end
This.SaveSession({count = count})
`, ScriptInstance{SessionID: "sess-b", ScriptRef: "Counter"}, Services{
		Commands: &helperTestSubmitter{},
		Finder:   helperTestFinder{},
		Bus:      helperTestBus{},
		Timers:   NewOSTimerService(),
		Sessions: sessions,
	})
	if err != nil {
		t.Fatalf("NewLuaVMWithInstance B: %v", err)
	}
	defer vmB.Stop()

	stateA, ok := sessions.LoadSession("sess-a")
	if !ok || !equalAnyNumber(stateA["count"], 1) {
		t.Fatalf("sess-a state = %#v, want count=1", stateA)
	}
	stateB, ok := sessions.LoadSession("sess-b")
	if !ok || !equalAnyNumber(stateB["count"], 1) {
		t.Fatalf("sess-b state = %#v, want count=1", stateB)
	}
}

func equalAnySlice(got any, want []int) bool {
	raw, err := json.Marshal(got)
	if err != nil {
		return false
	}
	var have []int
	if err := json.Unmarshal(raw, &have); err != nil {
		return false
	}
	if len(have) != len(want) {
		return false
	}
	for i := range have {
		if have[i] != want[i] {
			return false
		}
	}
	return true
}

func equalAnyNumber(got any, want int) bool {
	switch v := got.(type) {
	case int:
		return v == want
	case int64:
		return int(v) == want
	case int32:
		return int(v) == want
	case float32:
		return int(v) == want
	case float64:
		return int(v) == want
	default:
		return false
	}
}
