Feature: This — entity binding API
  Proves the EntityBinding (This) API works correctly for a bound entity
  without Lua.

  Background:
    Given a scripting environment with entity "switch-1" of domain "switch"

  Scenario: SendCommand on bound entity succeeds
    When I call This.SendCommand "turn_on" with no payload
    Then SendCommand returns a non-empty command ID
    And the command targets entity "switch-1"

  Scenario: SendCommand with payload includes data
    When I call This.SendCommand "set_brightness" with payload {"brightness":50}
    Then SendCommand returns a non-empty command ID
    And the recorded command payload contains brightness 50

  Scenario: SendEvent publishes an event
    Given I have a subscription on "*"
    When I call This.SendEvent "turned_on" with payload {}
    Then the subscription receives an event with type "turned_on"
    And the event originates from entity "switch-1"

  Scenario: OnEvent registers and receives events for this entity
    Given I call This.OnEvent "turned_on" with handler "h1"
    When an event is published to "switch-1" of type "turned_on"
    Then handler "h1" fires once

  Scenario: OnEvent does not fire for other entities
    Given I call This.OnEvent "turned_on" with handler "h1"
    When an event is published to "other-entity" of type "turned_on"
    Then handler "h1" fires 0 times

  Scenario: OnCommand registers and receives commands for this entity
    Given I call This.OnCommand "turn_on" with handler "h1"
    When a command "turn_on" is dispatched to entity "switch-1"
    Then handler "h1" fires once

  Scenario: OnCommand does not fire for other commands
    Given I call This.OnCommand "turn_on" with handler "h1"
    When a command "turn_off" is dispatched to entity "switch-1"
    Then handler "h1" fires 0 times

  Scenario: GetField returns the correct field value
    Given entity "switch-1" has field "state" set to "on"
    When I call This.GetField "state"
    Then GetField returns "on"

  Scenario: GetField returns empty string for unknown field
    When I call This.GetField "nonexistent"
    Then GetField returns ""

  Scenario: GetState returns current entity domain state
    Given entity "switch-1" has domain state set to {"state":"on"}
    When I call This.GetState
    Then GetState returns a map with key "state" value "on"
