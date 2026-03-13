Feature: Search across plugins

  Background:
    Given a running plugin "hue" with devices:
      | id         | name        |
      | hue-bridge | Hue Bridge  |
    And plugin "hue" has entities:
      | id       | device     | domain | name          |
      | light-1  | hue-bridge | light  | Ceiling Light |
      | light-2  | hue-bridge | light  | Desk Lamp     |
      | scene-1  | hue-bridge | scene  | Evening Mode  |

  Scenario: Search all entities returns all results
    When I search entities
    Then the response status is 200
    And the entity results contain 3 items

  Scenario: Filter entities by domain
    When I search entities with domain "light"
    Then the response status is 200
    And the entity results contain 2 items
    And every entity result has domain "light"

  Scenario: Filter entities by plugin and domain
    When I search entities with plugin "hue" and domain "scene"
    Then the response status is 200
    And the entity results contain 1 items
    And entity result "scene-1" exists

  Scenario: Search devices returns all devices for a plugin
    When I search devices with plugin "hue"
    Then the response status is 200
    And the device results contain 1 items

  Scenario: Search returns empty for unknown plugin
    When I search entities with plugin "no-such-plugin"
    Then the response status is 200
    And the entity results contain 0 items

  Scenario: Cross-plugin entity search spans all plugins
    Given a running plugin "zigbee" with devices:
      | id     | name     |
      | zb-hub | ZB Hub   |
    And plugin "zigbee" has entities:
      | id         | device | domain | name         |
      | zb-light-1 | zb-hub | light  | Garden Light |
    When I search entities with domain "light"
    Then the response status is 200
    And the entity results contain 3 items
    And entity results include plugin "hue"
    And entity results include plugin "zigbee"

  Scenario Outline: Domain filter returns only matching entities
    When I search entities with domain "<domain>"
    Then the entity results contain <count> items

    Examples:
      | domain | count |
      | light  | 2     |
      | scene  | 1     |
      | switch | 0     |
