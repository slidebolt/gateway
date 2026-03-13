Feature: Device EntityQuery — dynamic entity surfacing
  A device can carry an EntityQuery. When the gateway lists entities for that
  device it unions the plugin's directly-registered entities with all entities
  that match the query across the whole registry, deduplicating by entity ID.
  This lets a plugin (e.g. Alexa) surface any tagged entity without owning it.
  The system/stats endpoint reports the total device and entity counts known to
  the gateway across all registered plugins.

  # ---------------------------------------------------------------------------
  # Pass 1 – EntityQuery surfaces entities from other plugins
  # ---------------------------------------------------------------------------

  Scenario: Device with EntityQuery surfaces entities matching the query
    Given plugin "lights-plugin" is registered
    And plugin "alexa-plugin" is registered
    And plugin "lights-plugin" has device "basement" with entity "light-1" labeled "Expose:Alexa"
    And plugin "lights-plugin" has device "basement" with entity "light-2" labeled "Expose:Alexa"
    And plugin "lights-plugin" has device "basement" with entity "light-3" labeled "Expose:Local"
    And device "alexa-device" under plugin "alexa-plugin" has EntityQuery label "Expose:Alexa"
    When I list entities for plugin "alexa-plugin" device "alexa-device"
    Then the entity list contains "light-1"
    And the entity list contains "light-2"
    And the entity list does not contain "light-3"

  Scenario: Device EntityQuery unions with directly registered entities, no duplicates
    Given plugin "lights-plugin" is registered
    And plugin "alexa-plugin" is registered
    And plugin "lights-plugin" has device "basement" with entity "light-1" labeled "Expose:Alexa"
    And plugin "alexa-plugin" has device "alexa-device" with entity "direct-entity" labeled "Type:Direct"
    And device "alexa-device" under plugin "alexa-plugin" has EntityQuery label "Expose:Alexa"
    When I list entities for plugin "alexa-plugin" device "alexa-device"
    Then the entity list contains "light-1"
    And the entity list contains "direct-entity"
    And the entity list has no duplicate entity IDs

  Scenario: Getting a single entity via a device EntityQuery works
    Given plugin "lights-plugin" is registered
    And plugin "alexa-plugin" is registered
    And plugin "lights-plugin" has device "basement" with entity "light-1" labeled "Expose:Alexa"
    And device "alexa-device" under plugin "alexa-plugin" has EntityQuery label "Expose:Alexa"
    When I get entity "light-1" from plugin "alexa-plugin" device "alexa-device"
    Then the entity response has ID "light-1"

  Scenario: Entity not in query is not found via the device
    Given plugin "lights-plugin" is registered
    And plugin "alexa-plugin" is registered
    And plugin "lights-plugin" has device "basement" with entity "light-1" labeled "Expose:Local"
    And device "alexa-device" under plugin "alexa-plugin" has EntityQuery label "Expose:Alexa"
    When I get entity "light-1" from plugin "alexa-plugin" device "alexa-device"
    Then the response is 404

  # ---------------------------------------------------------------------------
  # Pass 2 – System stats
  # ---------------------------------------------------------------------------

  Scenario: System stats reports total device and entity counts
    Given plugin "stats-plugin-a" is registered
    And plugin "stats-plugin-b" is registered
    And plugin "stats-plugin-a" has device "device-a1" with entity "entity-a1" labeled "Type:Test"
    And plugin "stats-plugin-a" has device "device-a1" with entity "entity-a2" labeled "Type:Test"
    And plugin "stats-plugin-b" has device "device-b1" with entity "entity-b1" labeled "Type:Test"
    When I request the system stats
    Then the system has at least 2 devices
    And the system has at least 3 entities
