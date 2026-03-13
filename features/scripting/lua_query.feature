Feature: Lua QueryService.Scripting — queries from within Lua
  Proves that the QueryService bindings work correctly inside a Lua VM.

  Background:
    Given a scripting query environment with 3 plugins and mixed domains

  Scenario: Lua Find returns all entities matching domain
    Given a Lua script:
      """
      Results = {}
      function OnInit(ctx)
        local list = QueryService.Scripting.Find("?domain=switch")
        list:each(function(e)
          table.insert(Results, e.ID)
        end)
      end
      """
    When I start the Lua VM for entity "e-light-kitchen"
    Then the VM starts without error
    And the Lua global "Results" is a table with 2 entries

  Scenario: Lua FindOne returns a single entity
    Given a Lua script:
      """
      FoundID = ""
      function OnInit(ctx)
        local e = QueryService.Scripting.FindOne("?domain=light")
        if e ~= nil then
          FoundID = e.ID
        end
      end
      """
    When I start the Lua VM for entity "e-light-kitchen"
    Then the VM starts without error
    And the Lua global "FoundID" is not empty

  Scenario: Lua FindOne returns nil for no match
    Given a Lua script:
      """
      WasNil = false
      function OnInit(ctx)
        local e = QueryService.Scripting.FindOne("?domain=cover")
        WasNil = (e == nil)
      end
      """
    When I start the Lua VM for entity "e-light-kitchen"
    Then the VM starts without error
    And the Lua global "WasNil" is true

  Scenario: Lua EntityList Count is correct
    Given a Lua script:
      """
      SwitchCount = 0
      function OnInit(ctx)
        local list = QueryService.Scripting.Find("?domain=switch")
        SwitchCount = list:count()
      end
      """
    When I start the Lua VM for entity "e-light-kitchen"
    Then the VM starts without error
    And the Lua global "SwitchCount" equals 2

  Scenario: Lua EntityList Where filters results
    Given a Lua script:
      """
      FilteredCount = 0
      function OnInit(ctx)
        local list = QueryService.Scripting.Find("")
        local lights = list:where(function(e) return e.Domain == "light" end)
        FilteredCount = lights:count()
      end
      """
    When I start the Lua VM for entity "e-light-kitchen"
    Then the VM starts without error
    And the Lua global "FilteredCount" equals 2

  Scenario: Lua EntityList First returns an entity
    Given a Lua script:
      """
      FirstID = ""
      function OnInit(ctx)
        local list = QueryService.Scripting.Find("?domain=switch")
        local first = list:first()
        if first ~= nil then
          FirstID = first.ID
        end
      end
      """
    When I start the Lua VM for entity "e-light-kitchen"
    Then the VM starts without error
    And the Lua global "FirstID" is not empty
