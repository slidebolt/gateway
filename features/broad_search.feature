Feature: Broad range search across 10 simulated plugins

  # Label design:
  #   Plugin label = Plugin:<plugin-id>   (e.g. Plugin:Plugin-01)  — shared by all 100 entities in that plugin
  #   Device label = Device:<device-id>  (e.g. Device:Plugin-01-Device-01) — unique to that device's 10 entities
  #
  # Expected counts (1,000 total entities):
  #   name pattern "entity"            → 1,000  (every entity's name contains "entity")
  #   label Plugin:Plugin-01           →   100  (10 devices × 10 entities)
  #   label Device:Plugin-01-Device-01 →    10  (10 entities in that one device)

  Background:
    Given 10 simulated plugins each with 10 devices and 10 entities

  Scenario: Global name search via HTTP gateway returns all 1000 entities
    When I search entities matching "entity"
    Then the search returns 1000 entities

  Scenario: Label search by plugin returns 100 entities
    When I search entities with label "Plugin:Plugin-01"
    Then the search returns 100 entities

  Scenario: Label search by device returns 10 entities
    When I search entities with label "Device:Plugin-01-Device-01"
    Then the search returns 10 entities

  Scenario: Each plugin can query the aggregate directly and see all 1000 entities
    When plugin "plugin-01" queries the registry for entities matching "entity"
    Then the registry query returns 1000 entities

  Scenario: Direct registry query respects plugin label filter
    When plugin "plugin-05" queries the registry for entities with label "Plugin:Plugin-03"
    Then the registry query returns 100 entities

  Scenario: Direct registry query respects device label filter
    When plugin "plugin-09" queries the registry for entities with label "Device:Plugin-07-Device-05"
    Then the registry query returns 10 entities

  Scenario Outline: Plugin-scoped gateway search returns correct count
    When I search entities with label "Plugin:<plugin>"
    Then the search returns 100 entities

    Examples:
      | plugin    |
      | Plugin-01 |
      | Plugin-05 |
      | Plugin-10 |
