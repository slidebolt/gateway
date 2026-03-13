Feature: Command dispatch

  Background:
    Given a running plugin "smart-plug" with devices:
      | id     | name    |
      | plug-1 | Plug 1  |
    And plugin "smart-plug" has entities:
      | id       | device | domain | name          | actions              |
      | outlet-1 | plug-1 | switch | Kitchen Plug  | turn_on,turn_off     |

  Scenario: Send a command returns accepted with pending status
    When I send command "turn_on" to entity "outlet-1" on plugin "smart-plug" device "plug-1"
    Then the response status is 202
    And the command state is "pending"
    And the command has an ID

  Scenario: Poll command status by ID
    When I send command "turn_on" to entity "outlet-1" on plugin "smart-plug" device "plug-1"
    Then the response status is 202
    When I poll the command status
    Then the response status is 200
    And the command state is "succeeded"

  Scenario: Sending command to unknown entity returns 4xx
    When I send command "turn_on" to entity "no-entity" on plugin "smart-plug" device "plug-1"
    Then the response status is 404

  Scenario: Sending command to unknown plugin returns 4xx
    When I send command "turn_on" to entity "outlet-1" on plugin "ghost-plugin" device "plug-1"
    Then the response status is 404

  Scenario: Sending unsupported action returns 400
    When I send command "explode" to entity "outlet-1" on plugin "smart-plug" device "plug-1"
    Then the response status is 400
