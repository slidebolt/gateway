Feature: Lua CommandService.Scripting and This — command dispatch from Lua
  Proves that commands can be sent from Lua and This bindings work.

  Background:
    Given a scripting command environment with 1 plugin and 2 entities

  Scenario: Lua This.SendCommand dispatches a command
    Given a Lua script:
      """
      SentID = ""
      function OnInit(ctx)
        SentID = This.SendCommand("turn_on", {})
      end
      """
    When I start the Lua VM for entity "e1"
    Then the VM starts without error
    And the Lua global "SentID" is not empty
    And the recorded command on "e1" has name "turn_on"

  Scenario: Lua This.SendCommand with parameters
    Given a Lua script:
      """
      SentID = ""
      function OnInit(ctx)
        SentID = This.SendCommand("set_brightness", {brightness = 75})
      end
      """
    When I start the Lua VM for entity "e1"
    Then the VM starts without error
    And the recorded command payload contains brightness 75

  Scenario: Lua CommandService.Scripting.Send targets a specific entity
    Given a Lua script:
      """
      SentID = ""
      function OnInit(ctx)
        local list = QueryService.Scripting.Find("?domain=switch")
        local e = list:first()
        if e ~= nil then
          SentID = CommandService.Scripting.Send(e, "turn_on", {})
        end
      end
      """
    When I start the Lua VM for entity "e1"
    Then the VM starts without error
    And the Lua global "SentID" is not empty

  Scenario: Lua sends a command in response to an event
    Given a scripting event environment with test entities
    And a Lua script:
      """
      CommandsSent = 0
      function OnInit(ctx)
        EventService.Scripting.OnEvent(ctx, "entity-a.trigger", function(env)
          This.SendCommand("turn_on", {})
          CommandsSent = CommandsSent + 1
        end)
      end
      """
    When I start the Lua VM for entity "entity-a"
    And an event is published to subject "entity-a" of type "trigger"
    Then the Lua global "CommandsSent" equals 1
    And the recorded command on "entity-a" has name "turn_on"

  Scenario: Lua cascade — command triggers event triggers another command
    Given a scripting event environment with test entities
    And a Lua script for VM "controller":
      """
      Steps = 0
      function OnInit(ctx)
        EventService.Scripting.OnEvent(ctx, "entity-a.activate", function(env)
          Steps = Steps + 1
          This.SendCommand("turn_on", {})
          EventService.Scripting.Publish(ctx, "entity-b", "activated", {})
        end)
        EventService.Scripting.OnEvent(ctx, "entity-b.activated", function(env)
          Steps = Steps + 1
        end)
      end
      """
    When I start VM "controller" for entity "entity-a"
    And an event is published to subject "entity-a" of type "activate"
    Then VM "controller" has Lua global "Steps" equal to 2
