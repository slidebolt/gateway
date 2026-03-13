Feature: History recording

  Scenario: Fresh history store has zero counts
    When I get history stats
    Then the response status is 200
    And history event count is 0
    And history command count is 0

  Scenario: Gateway records system health
    When I check health
    Then the response status is 200
    And the response body contains "ok"
