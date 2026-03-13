Feature: Dynamic event subscriptions
  A dynamic subscription matches incoming EntityEventEnvelopes against an
  EventFilter{EntityQuery, Action}. Any event whose entity satisfies the query
  AND whose payload "type" satisfies the action filter is delivered to the
  subscriber. Filtering is evaluated per-event with no advance knowledge of
  which entities will exist, making subscriptions automatically include new
  entities that match in the future.

  # ---------------------------------------------------------------------------
  # Pass 1 — EntityQuery filtering
  # ---------------------------------------------------------------------------

  Scenario: Subscription receives events from entities matching a label query
    Given plugin "motion-plugin" is registered
    And plugin "motion-plugin" has entity "sensor-basement-1" with label "Location:Basement" and domain "motion_sensor"
    And plugin "motion-plugin" has entity "sensor-basement-2" with label "Location:Basement" and domain "motion_sensor"
    And plugin "motion-plugin" has entity "sensor-upstairs-1" with label "Location:Upstairs" and domain "motion_sensor"
    When I create a subscription with label query "Location:Basement"
    And event "activate" is ingested for entity "sensor-basement-1"
    And event "activate" is ingested for entity "sensor-basement-2"
    And event "activate" is ingested for entity "sensor-upstairs-1"
    Then the subscription has 2 buffered events

  Scenario: Subscription receives events matching an action filter
    Given plugin "light-plugin" is registered
    And plugin "light-plugin" has entity "light-1" with label "Room:Kitchen" and domain "light"
    When I create a subscription with label query "Room:Kitchen" and action "turn_on"
    And event "turn_on" is ingested for entity "light-1"
    And event "turn_off" is ingested for entity "light-1"
    And event "state" is ingested for entity "light-1"
    Then the subscription has 1 buffered events

  Scenario: Wildcard action receives all events from matching entities
    Given plugin "sensor-plugin" is registered
    And plugin "sensor-plugin" has entity "temp-1" with label "Type:Temperature" and domain "sensor"
    When I create a subscription with label query "Type:Temperature" and action "*"
    And event "reading" is ingested for entity "temp-1"
    And event "alert" is ingested for entity "temp-1"
    And event "calibrate" is ingested for entity "temp-1"
    Then the subscription has 3 buffered events

  Scenario: Subscription does not receive events from non-matching entities
    Given plugin "mixed-plugin" is registered
    And plugin "mixed-plugin" has entity "door-1" with label "Type:Door" and domain "sensor"
    And plugin "mixed-plugin" has entity "window-1" with label "Type:Window" and domain "sensor"
    When I create a subscription with label query "Type:Door"
    And event "open" is ingested for entity "door-1"
    And event "open" is ingested for entity "window-1"
    Then the subscription has 1 buffered events

  Scenario: Subscription filtered by domain receives only matching domain events
    Given plugin "home-plugin" is registered
    And plugin "home-plugin" has entity "bulb-1" with label "Room:Living" and domain "light"
    And plugin "home-plugin" has entity "motion-1" with label "Room:Living" and domain "motion_sensor"
    When I create a subscription with domain "light"
    And event "state" is ingested for entity "bulb-1"
    And event "activate" is ingested for entity "motion-1"
    Then the subscription has 1 buffered events

  # ---------------------------------------------------------------------------
  # Pass 2 — Lifecycle
  # ---------------------------------------------------------------------------

  Scenario: Deleting a subscription stops event delivery
    Given plugin "delete-plugin" is registered
    And plugin "delete-plugin" has entity "relay-1" with label "Type:Relay" and domain "switch"
    When I create a subscription with label query "Type:Relay"
    And event "on" is ingested for entity "relay-1"
    Then the subscription has 1 buffered events
    When I delete the subscription
    And event "on" is ingested for entity "relay-1"
    Then the subscription is gone

  Scenario: A new entity matching the query delivers events without re-subscribing
    Given plugin "dynamic-plugin" is registered
    And plugin "dynamic-plugin" has entity "light-old" with label "Floor:Ground" and domain "light"
    When I create a subscription with label query "Floor:Ground"
    And plugin "dynamic-plugin" registers a new entity "light-new" with label "Floor:Ground"
    And event "state" is ingested for entity "light-new"
    Then the subscription has 1 buffered events
