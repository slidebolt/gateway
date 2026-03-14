Feature: Entity meta store

  Background:
    Given a running plugin "sensor-plugin" with devices:
      | id      | name        |
      | device1 | Main Device |
    And plugin "sensor-plugin" has entities:
      | id       | device  | domain | name   | actions          |
      | entity-1 | device1 | switch | Light  | turn_on,turn_off |

  Scenario: Set a meta key on an entity
    When I patch meta on entity "entity-1" plugin "sensor-plugin" device "device1" with key "mykey" and value {"foo":"bar"}
    Then the response status is 200
    And the entity meta contains key "mykey" with value {"foo":"bar"}

  Scenario: Overwrite an existing meta key
    Given entity "entity-1" plugin "sensor-plugin" device "device1" has meta key "mykey" with value {"foo":"bar"}
    When I patch meta on entity "entity-1" plugin "sensor-plugin" device "device1" with key "mykey" and value {"foo":"updated"}
    Then the response status is 200
    And the entity meta contains key "mykey" with value {"foo":"updated"}

  Scenario: Patch merges multiple keys without removing existing ones
    Given entity "entity-1" plugin "sensor-plugin" device "device1" has meta key "key1" with value {"a":1}
    When I patch meta on entity "entity-1" plugin "sensor-plugin" device "device1" with key "key2" and value {"b":2}
    Then the response status is 200
    And the entity meta contains key "key1" with value {"a":1}
    And the entity meta contains key "key2" with value {"b":2}

  Scenario: Delete a meta key
    Given entity "entity-1" plugin "sensor-plugin" device "device1" has meta key "mykey" with value {"foo":"bar"}
    When I delete meta key "mykey" on entity "entity-1" plugin "sensor-plugin" device "device1"
    Then the response status is 204
    And the entity meta does not contain key "mykey"

  Scenario: Meta survives a label patch
    Given entity "entity-1" plugin "sensor-plugin" device "device1" has meta key "mykey" with value {"foo":"bar"}
    When I patch labels on entity "entity-1" plugin "sensor-plugin" device "device1" with "Room" "Kitchen"
    Then the entity meta contains key "mykey" with value {"foo":"bar"}

  Scenario: Meta round-trips through GET entity
    Given entity "entity-1" plugin "sensor-plugin" device "device1" has meta key "mykey" with value {"foo":"bar"}
    When I get entity "entity-1" on plugin "sensor-plugin" device "device1"
    Then the response status is 200
    And the entity meta contains key "mykey" with value {"foo":"bar"}

  Scenario: Patch meta on unknown entity returns 404
    When I patch meta on entity "no-entity" plugin "sensor-plugin" device "device1" with key "mykey" and value {"foo":"bar"}
    Then the response status is 404

  Scenario: Patch meta with empty body returns 400
    When I patch meta on entity "entity-1" plugin "sensor-plugin" device "device1" with empty body
    Then the response status is 400
