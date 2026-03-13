Feature: QueryService.Scripting — ergonomic entity search
  Proves the scripting query API works correctly before any Lua is involved.
  Steps call the Go QueryScripting methods directly.

  Background:
    Given a scripting query environment with 3 plugins and mixed domains

  Scenario: Find all entities matching a domain
    When I call QueryService.Scripting.Find with "?domain=switch"
    Then the result contains 2 entities
    And every result entity has domain "switch"

  Scenario: Find entities by label
    When I call QueryService.Scripting.Find with "?label=Room:Kitchen"
    Then the result contains 3 entities

  Scenario: Find entities by pattern
    When I call QueryService.Scripting.Find with "?pattern=*bulb*"
    Then the result contains 2 entities

  Scenario: Find with multiple filters combined
    When I call QueryService.Scripting.Find with "?domain=light&label=Room:Kitchen"
    Then the result contains 1 entity
    And every result entity has domain "light"

  Scenario: FindOne returns the first match
    When I call QueryService.Scripting.FindOne with "?domain=switch"
    Then FindOne returns a single entity with domain "switch"

  Scenario: FindOne returns nil for no match
    When I call QueryService.Scripting.FindOne with "?domain=cover"
    Then FindOne returns nil

  Scenario: Find with empty query returns all entities
    When I call QueryService.Scripting.Find with ""
    Then the result contains at least 1 entity

  Scenario: EntityList.Each iterates all results
    When I call QueryService.Scripting.Find with "?domain=switch"
    Then iterating with Each visits 2 entities

  Scenario: EntityList.Where filters in Go space
    When I call QueryService.Scripting.Find with ""
    And I filter the list keeping only domain "light"
    Then the result contains only "light" domain entities

  Scenario: EntityList.First returns first element
    When I call QueryService.Scripting.Find with "?domain=switch"
    Then First returns a non-nil entity

  Scenario: EntityList.Count is correct
    When I call QueryService.Scripting.Find with "?domain=switch"
    Then Count returns 2
