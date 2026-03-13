Feature: EventService.Scripting — subscription and publish API
  Proves event fan-out, subject translation, and subscription lifecycle
  without Lua.

  Background:
    Given a scripting event environment with test entities

  Scenario: OnEvent by entity ID receives events for that entity
    Given I subscribe with "entity-a.light.turn_on"
    When an event is published to subject "entity-a" of type "light.turn_on"
    Then the subscription callback fires once

  Scenario: OnEvent wildcard entity receives all events
    Given I subscribe with "*"
    When an event is published to subject "entity-a" of type "light.turn_on"
    And an event is published to subject "entity-b" of type "switch.turn_off"
    Then the subscription callback fires 2 times

  Scenario: OnEvent domain filter only receives matching domain events
    Given I subscribe with "?domain=light"
    When an event is published to subject "entity-a" of type "light.turn_on"
    And an event is published to subject "entity-b" of type "switch.turn_off"
    Then the subscription callback fires once

  Scenario: OnEvent label filter only receives entities with that label
    Given entity "entity-a" has label "Room:Kitchen"
    And I subscribe with "?label=Room:Kitchen"
    When an event is published to subject "entity-a" of type "light.turn_on"
    And an event is published to subject "entity-b" of type "light.turn_on"
    Then the subscription callback fires once

  Scenario: Two subscribers on same subject both receive events
    Given I subscribe with "entity-a.*" as "sub1"
    And I subscribe with "entity-a.*" as "sub2"
    When an event is published to subject "entity-a" of type "light.turn_on"
    Then subscription "sub1" fires once
    And subscription "sub2" fires once

  Scenario: Unsubscribe stops delivery
    Given I subscribe with "*" as "sub1"
    When I unsubscribe "sub1"
    And an event is published to subject "entity-a" of type "light.turn_on"
    Then subscription "sub1" fires 0 times

  Scenario: No pre-subscription events delivered
    When an event is published to subject "entity-a" of type "light.turn_on"
    Then subscribing afterwards receives 0 historical events

  Scenario: Publish delivers to all subscribers
    Given I subscribe with "*" as "sub1"
    And I subscribe with "*" as "sub2"
    When I publish an event for "entity-a" type "light.turn_on"
    Then subscription "sub1" fires once
    And subscription "sub2" fires once
