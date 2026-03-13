Feature: Lua integration — multiple VMs, full system scenarios
  End-to-end tests with many VMs interacting through events and commands.

  Background:
    Given a scripting event environment with test entities

  Scenario: 10 VMs all receive the same broadcast event
    Given 10 Lua VMs each subscribing to wildcard events
    When an event is published to subject "entity-a" of type "broadcast"
    Then all 10 VMs received the event exactly once

  Scenario: 10 VMs each send a command, all are recorded
    Given 10 Lua VMs each sending a command on init
    Then all 10 commands were recorded

  Scenario: This.GetState returns entity state from Lua
    Given entity "entity-a" has domain state set to {"state":"on","brightness":80}
    And a Lua script:
      """
      State = ""
      Brightness = 0
      function OnInit(ctx)
        local s = This.GetState()
        if s ~= nil then
          State = s.state or ""
          Brightness = s.brightness or 0
        end
      end
      """
    When I start the Lua VM for entity "entity-a"
    Then the VM starts without error
    And the Lua global "State" equals "on"
    And the Lua global "Brightness" equals 80

  Scenario: This.GetField returns a single field from Lua
    Given entity "entity-a" has field "state" set to "off"
    And a Lua script:
      """
      FieldVal = ""
      function OnInit(ctx)
        FieldVal = This.GetField("state")
      end
      """
    When I start the Lua VM for entity "entity-a"
    Then the VM starts without error
    And the Lua global "FieldVal" equals "off"

  Scenario: This.OnEvent fires only for this entity
    Given a Lua script:
      """
      OwnEvents = 0
      function OnInit(ctx)
        This.OnEvent(ctx, function(env)
          OwnEvents = OwnEvents + 1
        end)
      end
      """
    When I start the Lua VM for entity "entity-a"
    And an event is published to subject "entity-a" of type "light.turn_on"
    And an event is published to subject "entity-b" of type "light.turn_on"
    Then the Lua global "OwnEvents" equals 1

  Scenario: This.OnCommand fires for the registered command
    Given a Lua script:
      """
      HandledCount = 0
      function OnInit(ctx)
        This.OnCommand("turn_on", function(cmd)
          HandledCount = HandledCount + 1
        end)
      end
      """
    When I start the Lua VM for entity "entity-a"
    And I dispatch command "turn_on" to entity "entity-a" via the binding
    Then the Lua global "HandledCount" equals 1

  Scenario: This.OnCommand does not fire for a different command
    Given a Lua script:
      """
      HandledCount = 0
      function OnInit(ctx)
        This.OnCommand("turn_on", function(cmd)
          HandledCount = HandledCount + 1
        end)
      end
      """
    When I start the Lua VM for entity "entity-a"
    And I dispatch command "turn_off" to entity "entity-a" via the binding
    Then the Lua global "HandledCount" equals 0
