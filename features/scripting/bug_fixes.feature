Feature: Scripting Bug Fixes and Robustness
  Verify fixes for reported scripting bugs and ensure system stability.

  Background:
    Given a scripting event environment with test entities

  Scenario: Updating a script using timers does not panic (Replacement Stability)
    Given a Lua script:
      """
      function OnInit(ctx)
        TimerService.Scripting.Every(0.1, function() end)
      end
      """
    And I start the Lua VM
    Then the VM starts without error
    When I update the Lua script:
      """
      function OnInit(ctx)
        print("Updated script")
      end
      """
    Then the VM starts without error

  Scenario: OnEvent handles colon-syntax and missing subject (Robustness)
    Given a Lua script:
      """
      EventCount = 0
      function OnInit(ctx)
        -- Test colon syntax and automatic subject discovery
        This:OnEvent(function(evt)
          EventCount = EventCount + 1
        end)
      end
      """
    When I start the Lua VM
    And an event is published to "entity-a" of type "test-event"
    Then the Lua global "EventCount" eventually equals 1

  Scenario: Lua tables as sequences are encoded as JSON arrays
    Given a Lua script:
      """
      function OnInit(ctx)
        -- Send a command with a sequence table (array)
        This.SendCommand("set_rgb", { rgb = {255, 128, 64} })
      end
      """
    When I start the Lua VM
    Then the recorded command on "entity-a" with domain "light" has RGB array [255, 128, 64]

  Scenario: Entity Actions are exposed to Lua
    Given a Lua script:
      """
      HasSetRGB = false
      function OnInit(ctx)
        local lights = QueryService.Scripting.Find("?domain=light")
        lights:each(function(e)
          if e.Actions then
            for _, a in ipairs(e.Actions) do
              if a == "set_rgb" then HasSetRGB = true end
            end
          end
        end)
      end
      """
    When I start the Lua VM
    Then the Lua global "HasSetRGB" is true

