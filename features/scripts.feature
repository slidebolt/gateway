Feature: Lua script management and execution

  Background:
    Given a running plugin "automations" with devices:
      | id   | name   |
      | hub  | Hub    |
    And plugin "automations" has entities:
      | id      | device | domain | name        |
      | light-a | hub    | light  | Ambient LED |

  Scenario: Store and retrieve a Lua script
    Given plugin "automations" stores a script for entity "light-a":
      """
      return "hello from lua"
      """
    When I get the script for entity "light-a" on plugin "automations"
    Then the response status is 200
    And the script body contains "hello from lua"

  Scenario: Execute a Lua script returns a result
    Given plugin "automations" stores a script for entity "light-a":
      """
      local state = entity_state
      if state == "on" then
        return "already on"
      else
        return "turn_on"
      end
      """
    When I execute the script for entity "light-a" on plugin "automations" with state "off"
    Then the lua result is "turn_on"

  Scenario: Execute Lua with on state
    Given plugin "automations" stores a script for entity "light-a":
      """
      local state = entity_state
      if state == "on" then
        return "already on"
      else
        return "turn_on"
      end
      """
    When I execute the script for entity "light-a" on plugin "automations" with state "on"
    Then the lua result is "already on"

  Scenario: Delete a script
    Given plugin "automations" stores a script for entity "light-a":
      """
      return "bye"
      """
    When I delete the script for entity "light-a" on plugin "automations"
    Then the response status is 200
    When I get the script for entity "light-a" on plugin "automations"
    Then the response status is 404

  Scenario: Get nonexistent script returns 404
    When I get the script for entity "nonexistent" on plugin "automations"
    Then the response status is 404
