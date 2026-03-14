Feature: Lua Timers — After, Every, and Cancel
  Test the TimerService.Scripting API in Lua VMs.

  Background:
    Given a scripting event environment with test entities

  Scenario: TimerService.After executes once after a delay
    Given a Lua script:
      """
      TimerFired = 0
      function OnInit(ctx)
        TimerService.Scripting.After(0.5, function()
          TimerFired = TimerFired + 1
        end)
      end
      """
    When I start the Lua VM
    Then the VM starts without error
    And the Lua global "TimerFired" equals 0
    And the Lua global "TimerFired" eventually equals 1
    And I wait 1 seconds
    And the Lua global "TimerFired" equals 1

  Scenario: TimerService.Every executes repeatedly
    Given a Lua script:
      """
      TickCount = 0
      function OnInit(ctx)
        TimerService.Scripting.Every(0.2, function()
          TickCount = TickCount + 1
        end)
      end
      """
    When I start the Lua VM
    Then the VM starts without error
    And the Lua global "TickCount" eventually equals 3

  Scenario: TimerService.Cancel stops a recurring timer
    Given a Lua script:
      """
      TickCount = 0
      TimerID = 0
      function OnInit(ctx)
        TimerID = TimerService.Scripting.Every(0.2, function()
          TickCount = TickCount + 1
        end)
      end
      
      function StopTimer()
        TimerService.Scripting.Cancel(TimerID)
      end
      """
    When I start the Lua VM
    And the Lua global "TickCount" eventually equals 2
    And I execute the Lua code "StopTimer()"
    And I wait 1 seconds
    Then the Lua global "TickCount" equals 2

  Scenario: Party Time simulation with labels and timers
    Given entity "bulb-1" has label "area:basement"
    And entity "bulb-2" has label "area:basement"
    And entity "bulb-3" has label "area:basement"
    And a Lua script:
      """
      function OnInit(ctx)
        -- Change color of all basement bulbs every 0.2s
        -- Use 100% probability for predictable testing
        TimerService.Scripting.Every(0.2, function()
          local lights = QueryService.Scripting.Find("?label=area:basement")
          lights:each(function(e)
            CommandService.Scripting.Send(e, "set_rgb", {r=255, g=0, b=0})
          end)
        end)
      end
      """
    When I start the Lua VM
    And I wait 1 seconds
    Then the recorded command count is at least 9
