Feature: Plugin lifecycle

  Scenario: Registered plugin appears in plugin list
    Given a running plugin "hue"
    When I list all plugins
    Then the response status is 200
    And the plugin list contains "hue"

  Scenario: Multiple plugins all appear in plugin list
    Given a running plugin "hue"
    And a running plugin "zigbee"
    And a running plugin "zwave"
    When I list all plugins
    Then the response status is 200
    And the plugin list contains "hue"
    And the plugin list contains "zigbee"
    And the plugin list contains "zwave"

  Scenario: Gateway returns 403 for an unregistered plugin
    When I list devices for plugin "ghost-plugin"
    Then the response status is 403

  Scenario: Plugin health check returns ok
    Given a running plugin "matter"
    When I check health for plugin "matter"
    Then the response status is 200

  Scenario: Plugin search returns manifests for all running plugins
    Given a running plugin "alpha"
    And a running plugin "beta"
    And a running plugin "gamma"
    When I search for plugins
    Then the response status is 200
    And the results contain at least 3 manifests
