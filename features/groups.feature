Feature: Group entities — command fan-out
  A group entity is a regular entity with a CommandQuery. When it receives a
  command the gateway fans the command out to every entity matching the query
  in addition to dispatching to the owning plugin (if any). Groups are
  transparent: they appear in search results under their own domain, chain
  recursively, and detect cycles.

  # ---------------------------------------------------------------------------
  # Pass 1 — basic fan-out
  # ---------------------------------------------------------------------------

  Scenario: Command to a group fans out to all matching members
    Given a running plugin "lights-plugin" with devices:
      | id      | name         |
      | device1 | Main Device  |
    And plugin "lights-plugin" has entities:
      | id      | device  | domain | name    | actions           |
      | bulb-01 | device1 | light  | Bulb 1  | turn_on,turn_off  |
      | bulb-02 | device1 | light  | Bulb 2  | turn_on,turn_off  |
      | bulb-03 | device1 | light  | Bulb 3  | turn_on,turn_off  |
    And a group entity "basement-group" with domain "light" and CommandQuery label "Room:Basement"
    And entities "bulb-01,bulb-02,bulb-03" on plugin "lights-plugin" have label "Room:Basement"
    When I send command "turn_on" to group entity "basement-group"
    Then the response status is 202
    And the group command state is "succeeded"
    And all members with label "Room:Basement" received command "turn_on"

  Scenario: Command to a group with no matching members still succeeds
    Given a group entity "empty-group" with domain "light" and CommandQuery label "Room:NoSuchRoom"
    When I send command "turn_on" to group entity "empty-group"
    Then the response status is 202
    And the group command state is "succeeded"

  # ---------------------------------------------------------------------------
  # Pass 2 — transparent domain
  # ---------------------------------------------------------------------------

  Scenario: Group entity appears as its domain type in search results
    Given a group entity "basement-group" with domain "light" and CommandQuery label "Room:Basement"
    When I search entities with domain "light"
    Then the entity results include "basement-group"

  Scenario: Group entity appears in name pattern search
    Given a group entity "basement-lights" with domain "light" and CommandQuery label "Room:Basement"
    When I search entities matching "basement"
    Then the entity results include "basement-lights"

  # ---------------------------------------------------------------------------
  # Pass 3 — nested / chained groups
  # ---------------------------------------------------------------------------

  Scenario: Nested groups chain commands to leaf entities
    Given a running plugin "floor-plugin" with devices:
      | id     | name    |
      | dev-a  | Dev A   |
    And plugin "floor-plugin" has entities:
      | id      | device | domain | name    | actions           |
      | bulb-a1 | dev-a  | light  | A1      | turn_on,turn_off  |
      | bulb-a2 | dev-a  | light  | A2      | turn_on,turn_off  |
      | bulb-b1 | dev-a  | light  | B1      | turn_on,turn_off  |
      | bulb-b2 | dev-a  | light  | B2      | turn_on,turn_off  |
    And entities "bulb-a1,bulb-a2" on plugin "floor-plugin" have label "Room:RoomA"
    And entities "bulb-b1,bulb-b2" on plugin "floor-plugin" have label "Room:RoomB"
    And a group entity "group-room-a" with domain "light" and CommandQuery label "Room:RoomA" and label "Floor:Ground"
    And a group entity "group-room-b" with domain "light" and CommandQuery label "Room:RoomB" and label "Floor:Ground"
    And a group entity "all-floor" with domain "light" and CommandQuery label "Floor:Ground"
    When I send command "turn_on" to group entity "all-floor"
    Then the response status is 202
    And eventually all members with label "Room:RoomA" received command "turn_on"
    And eventually all members with label "Room:RoomB" received command "turn_on"

  # ---------------------------------------------------------------------------
  # Pass 4 — cycle detection
  # ---------------------------------------------------------------------------

  Scenario: Circular group references do not cause an infinite loop
    Given a group entity "cycle-a" with domain "light" and CommandQuery label "CycleGroup:test"
    And a group entity "cycle-b" with domain "light" and CommandQuery label "CycleGroup:test" and label "CycleGroup:test"
    And group entity "cycle-a" also has label "CycleGroup:test"
    When I send command "turn_on" to group entity "cycle-a"
    Then the response status is 202
    And the group command state is "succeeded"

  # ---------------------------------------------------------------------------
  # Pass 5 — plugin-owned group
  # ---------------------------------------------------------------------------

  Scenario: Plugin-owned group entity fans out and also dispatches to the plugin
    Given a running plugin "hue" with devices:
      | id     | name      |
      | bridge | Hue Bridge |
    And plugin "hue" has entities:
      | id       | device | domain | name     | actions           |
      | hue-01   | bridge | light  | Hue 1    | turn_on,turn_off  |
      | hue-02   | bridge | light  | Hue 2    | turn_on,turn_off  |
      | hue-03   | bridge | light  | Hue 3    | turn_on,turn_off  |
    And entities "hue-01,hue-02,hue-03" on plugin "hue" have label "HueGroup:lounge"
    And plugin "hue" also registers a group entity "hue-lounge" with domain "light" and CommandQuery label "HueGroup:lounge"
    When I send command "turn_on" to entity "hue-lounge" on plugin "hue" device "bridge"
    Then the response status is 202
    And all members with label "HueGroup:lounge" received command "turn_on"
    And plugin "hue" received the command for entity "hue-lounge"
