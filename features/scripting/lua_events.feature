Feature: Lua EventService.Scripting — subscriptions from within Lua
  Proves that event subscriptions registered in OnInit fire correctly.

  Background:
    Given a scripting event environment with test entities

  Scenario: Lua OnEvent receives a matching event
    Given a Lua script:
      """
      ReceivedCount = 0
      function OnInit(ctx)
        EventService.Scripting.OnEvent(ctx, "entity-a.light.turn_on", function(env)
          ReceivedCount = ReceivedCount + 1
        end)
      end
      """
    When I start the Lua VM for entity "entity-a"
    And an event is published to subject "entity-a" of type "light.turn_on"
    Then the Lua global "ReceivedCount" equals 1

  Scenario: Lua OnEvent wildcard receives all events
    Given a Lua script:
      """
      ReceivedCount = 0
      function OnInit(ctx)
        EventService.Scripting.OnEvent(ctx, "*", function(env)
          ReceivedCount = ReceivedCount + 1
        end)
      end
      """
    When I start the Lua VM for entity "entity-a"
    And an event is published to subject "entity-a" of type "light.turn_on"
    And an event is published to subject "entity-b" of type "switch.turn_off"
    Then the Lua global "ReceivedCount" equals 2

  Scenario: Lua OnEvent domain filter only fires for matching domain
    Given a Lua script:
      """
      LightCount = 0
      function OnInit(ctx)
        EventService.Scripting.OnEvent(ctx, "?domain=light", function(env)
          LightCount = LightCount + 1
        end)
      end
      """
    When I start the Lua VM for entity "entity-a"
    And an event is published to subject "entity-a" of type "light.turn_on"
    And an event is published to subject "entity-b" of type "switch.turn_off"
    Then the Lua global "LightCount" equals 1

  Scenario: Two Lua VMs each receive their own events
    Given a Lua script for VM "vm-a":
      """
      CountA = 0
      function OnInit(ctx)
        EventService.Scripting.OnEvent(ctx, "entity-a.*", function(env)
          CountA = CountA + 1
        end)
      end
      """
    And a Lua script for VM "vm-b":
      """
      CountB = 0
      function OnInit(ctx)
        EventService.Scripting.OnEvent(ctx, "entity-b.*", function(env)
          CountB = CountB + 1
        end)
      end
      """
    When I start VM "vm-a" for entity "entity-a"
    And I start VM "vm-b" for entity "entity-b"
    And an event is published to subject "entity-a" of type "light.turn_on"
    And an event is published to subject "entity-b" of type "switch.turn_off"
    Then VM "vm-a" has Lua global "CountA" equal to 1
    And VM "vm-a" has Lua global "CountB" equal to 0
    And VM "vm-b" has Lua global "CountB" equal to 1
    And VM "vm-b" has Lua global "CountA" equal to 0

  Scenario: Lua VM receives events sent by another Lua VM
    Given a Lua script for VM "sender":
      """
      function OnInit(ctx)
        EventService.Scripting.OnEvent(ctx, "entity-a.trigger", function(env)
          EventService.Scripting.Publish(ctx, "entity-b", "forwarded", {})
        end)
      end
      """
    And a Lua script for VM "receiver":
      """
      ReceivedForwarded = 0
      function OnInit(ctx)
        EventService.Scripting.OnEvent(ctx, "entity-b.forwarded", function(env)
          ReceivedForwarded = ReceivedForwarded + 1
        end)
      end
      """
    When I start VM "sender" for entity "entity-a"
    And I start VM "receiver" for entity "entity-b"
    And an event is published to subject "entity-a" of type "trigger"
    Then VM "receiver" has Lua global "ReceivedForwarded" equal to 1
