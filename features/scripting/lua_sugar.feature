Feature: Lua Sugar API — entity capability checks, safe send, and action filtering
  Proves the "Sugar" convenience layer: e:Supports, failOnError-safe Send,
  and ?action= query filtering.

  Background:
    Given a scripting environment with capability-aware entities

  # ---------------------------------------------------------------------------
  # e:Supports — entity-level capability check
  # ---------------------------------------------------------------------------

  Scenario: e:Supports returns true for a supported action
    Given a Lua script:
      """
      SupportsRGB = false
      function OnInit(ctx)
        local e = QueryService.Scripting.FindOne("?domain=light")
        if e ~= nil then
          SupportsRGB = e:Supports("set_rgb")
        end
      end
      """
    When I start the Lua VM for entity "e-rgb-light"
    Then the VM starts without error
    And the Lua global "SupportsRGB" is true

  Scenario: e:Supports returns false for an unsupported action
    Given a Lua script:
      """
      SupportsRGB = false
      function OnInit(ctx)
        local e = QueryService.Scripting.FindOne("?domain=switch")
        if e ~= nil then
          SupportsRGB = e:Supports("set_rgb")
        end
      end
      """
    When I start the Lua VM for entity "e-rgb-light"
    Then the VM starts without error
    And the Lua global "SupportsRGB" is false

  Scenario: e:Supports returns true when entity has no capability data (pass-through)
    Given a Lua script:
      """
      SupportsAny = false
      function OnInit(ctx)
        local e = QueryService.Scripting.FindOne("?domain=sensor")
        if e ~= nil then
          SupportsAny = e:Supports("any_unknown_action")
        end
      end
      """
    When I start the Lua VM for entity "e-rgb-light"
    Then the VM starts without error
    And the Lua global "SupportsAny" is true

  # ---------------------------------------------------------------------------
  # This.SendCommand — failOnError=false (default) silently skips unsupported
  # ---------------------------------------------------------------------------

  Scenario: This.SendCommand silently skips an unsupported action by default
    Given a Lua script:
      """
      WasSent = false
      function OnInit(ctx)
        local id = This.SendCommand("set_rgb", {})
        WasSent = (id ~= nil)
      end
      """
    When I start the Lua VM for entity "e-switch"
    Then the VM starts without error
    And the Lua global "WasSent" is false
    And no command was recorded for entity "e-switch"

  Scenario: This.SendCommand sends normally when action is supported
    Given a Lua script:
      """
      WasSent = false
      function OnInit(ctx)
        local id = This.SendCommand("turn_on", {})
        WasSent = (id ~= nil)
      end
      """
    When I start the Lua VM for entity "e-switch"
    Then the VM starts without error
    And the Lua global "WasSent" is true
    And the recorded command on "e-switch" has name "turn_on"

  Scenario: This.SendCommand with failOnError=true forces send even for unsupported action
    Given a Lua script:
      """
      WasSent = false
      function OnInit(ctx)
        local id = This.SendCommand("set_rgb", {}, {failOnError = true})
        WasSent = (id ~= nil)
      end
      """
    When I start the Lua VM for entity "e-switch"
    Then the VM starts without error
    And the Lua global "WasSent" is true

  # ---------------------------------------------------------------------------
  # CommandService.Scripting.Send — safe skip when entity lacks action
  # ---------------------------------------------------------------------------

  Scenario: CommandService.Scripting.Send skips when entity does not support action
    Given a Lua script:
      """
      WasSent = false
      function OnInit(ctx)
        local e = QueryService.Scripting.FindOne("?domain=switch")
        local id = CommandService.Scripting.Send(e, "set_rgb", {})
        WasSent = (id ~= nil)
      end
      """
    When I start the Lua VM for entity "e-rgb-light"
    Then the VM starts without error
    And the Lua global "WasSent" is false

  Scenario: CommandService.Scripting.Send sends when entity supports action
    Given a Lua script:
      """
      WasSent = false
      function OnInit(ctx)
        local e = QueryService.Scripting.FindOne("?domain=switch")
        local id = CommandService.Scripting.Send(e, "turn_on", {})
        WasSent = (id ~= nil)
      end
      """
    When I start the Lua VM for entity "e-rgb-light"
    Then the VM starts without error
    And the Lua global "WasSent" is true

  # ---------------------------------------------------------------------------
  # Find("?action=...") — action-based entity filtering
  # Note: entities with no Actions field pass through all action filters.
  # e-bare (domain=sensor, no Actions) is included in the background.
  # ---------------------------------------------------------------------------

  Scenario: Find with ?action= returns entities supporting or passing through that action
    Given a Lua script:
      """
      RGBCount = 0
      function OnInit(ctx)
        -- e-rgb-light has set_rgb; e-switch lacks it; e-bare (no Actions) passes through
        local list = QueryService.Scripting.Find("?action=set_rgb")
        RGBCount = list:count()
      end
      """
    When I start the Lua VM for entity "e-rgb-light"
    Then the VM starts without error
    And the Lua global "RGBCount" equals 2

  Scenario: Find with domain and action filters excludes entity with Actions but lacking the action
    Given a Lua script:
      """
      MatchCount = 0
      function OnInit(ctx)
        -- e-switch has Actions=[turn_on, turn_off], so set_rgb excludes it
        -- domain=switch filter removes e-bare pass-through too
        local list = QueryService.Scripting.Find("?domain=switch&action=set_rgb")
        MatchCount = list:count()
      end
      """
    When I start the Lua VM for entity "e-rgb-light"
    Then the VM starts without error
    And the Lua global "MatchCount" equals 0

  Scenario: Find with ?action= returns all entities when all support or pass through the action
    Given a Lua script:
      """
      TurnOnCount = 0
      function OnInit(ctx)
        -- e-rgb-light, e-switch both have turn_on; e-bare passes through
        local list = QueryService.Scripting.Find("?action=turn_on")
        TurnOnCount = list:count()
      end
      """
    When I start the Lua VM for entity "e-rgb-light"
    Then the VM starts without error
    And the Lua global "TurnOnCount" equals 3
