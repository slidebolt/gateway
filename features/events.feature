Feature: Entity events and state management
  Proves that every domain type can publish state-change events, that entity
  state (Reported / Effective / SyncStatus) is updated correctly, that the
  gateway aggregate reflects those changes, and that any plugin or observer can
  subscribe to the event bus to react in real-time.

  Background:
    Given a 10-plugin events environment is running
    # Plugins and their entities:
    #   event-switch-01 / event-switch-02  – domain: switch      (device: main-device, entity: main-entity)
    #   event-light-01  / event-light-02   – domain: light        (same naming)
    #   event-rgb-01    / event-rgb-02     – domain: light.rgb
    #   event-temp-01   / event-temp-02    – domain: sensor.temperature
    #   event-motion-01 / event-motion-02  – domain: binary_sensor

  # ── Switch ──────────────────────────────────────────────────────────────────

  Scenario: Switch entity reports on state via event
    When plugin "event-switch-01" ingests event {"type":"turn_on","on":true} for entity "main-entity"
    Then entity "main-entity" on plugin "event-switch-01" has sync_status "synced"
    And entity "main-entity" on plugin "event-switch-01" has state field "on" equal to "true"
    And entity "main-entity" on plugin "event-switch-01" has state field "type" equal to "turn_on"

  Scenario: Switch entity reports off state via event
    When plugin "event-switch-01" ingests event {"type":"turn_off","on":false} for entity "main-entity"
    Then entity "main-entity" on plugin "event-switch-01" has sync_status "synced"
    And entity "main-entity" on plugin "event-switch-01" has state field "on" equal to "false"

  Scenario: Switch command-and-event full cycle
    When I send command "turn_on" to entity "main-entity" on plugin "event-switch-02" device "main-device"
    Then the command state is "pending"
    When plugin "event-switch-02" ingests event {"type":"turn_on","on":true} for entity "main-entity" correlated with the last command
    Then entity "main-entity" on plugin "event-switch-02" has sync_status "synced"
    And entity "main-entity" on plugin "event-switch-02" last command ID matches the sent command
    When plugin "event-switch-02" ingests event {"type":"turn_off","on":false} for entity "main-entity"
    Then entity "main-entity" on plugin "event-switch-02" has state field "on" equal to "false"

  # ── Light ────────────────────────────────────────────────────────────────────

  Scenario: Light entity turns on with brightness
    When plugin "event-light-01" ingests event {"type":"turn_on","on":true,"brightness":100} for entity "main-entity"
    Then entity "main-entity" on plugin "event-light-01" has sync_status "synced"
    And entity "main-entity" on plugin "event-light-01" has state field "on" equal to "true"
    And entity "main-entity" on plugin "event-light-01" has state field "brightness" equal to "100"

  Scenario: Light brightness adjusts via event
    When plugin "event-light-01" ingests event {"type":"turn_on","on":true,"brightness":100} for entity "main-entity"
    And plugin "event-light-01" ingests event {"type":"state","on":true,"brightness":45} for entity "main-entity"
    Then entity "main-entity" on plugin "event-light-01" has state field "brightness" equal to "45"

  Scenario: Light turns off
    When plugin "event-light-01" ingests event {"type":"turn_on","on":true,"brightness":80} for entity "main-entity"
    And plugin "event-light-01" ingests event {"type":"turn_off","on":false,"brightness":0} for entity "main-entity"
    Then entity "main-entity" on plugin "event-light-01" has state field "on" equal to "false"
    And entity "main-entity" on plugin "event-light-01" has state field "brightness" equal to "0"

  Scenario: Light command-and-event cycle
    When I send command "turn_on" to entity "main-entity" on plugin "event-light-02" device "main-device"
    Then the command state is "pending"
    When plugin "event-light-02" ingests event {"type":"turn_on","on":true,"brightness":100} for entity "main-entity" correlated with the last command
    Then entity "main-entity" on plugin "event-light-02" has sync_status "synced"
    And entity "main-entity" on plugin "event-light-02" last command ID matches the sent command

  # ── RGB Light ────────────────────────────────────────────────────────────────

  Scenario: RGB light reports a colour change
    When plugin "event-rgb-01" ingests event {"type":"set_color","on":true,"r":255,"g":128,"b":0} for entity "main-entity"
    Then entity "main-entity" on plugin "event-rgb-01" has sync_status "synced"
    And entity "main-entity" on plugin "event-rgb-01" has state field "r" equal to "255"
    And entity "main-entity" on plugin "event-rgb-01" has state field "g" equal to "128"
    And entity "main-entity" on plugin "event-rgb-01" has state field "b" equal to "0"

  Scenario: RGB light colour update overwrites previous state
    When plugin "event-rgb-01" ingests event {"type":"set_color","on":true,"r":255,"g":128,"b":0} for entity "main-entity"
    And plugin "event-rgb-01" ingests event {"type":"set_color","on":true,"r":0,"g":255,"b":64} for entity "main-entity"
    Then entity "main-entity" on plugin "event-rgb-01" has state field "r" equal to "0"
    And entity "main-entity" on plugin "event-rgb-01" has state field "g" equal to "255"
    And entity "main-entity" on plugin "event-rgb-01" has state field "b" equal to "64"

  Scenario: RGB light command-and-event cycle
    When I send command "set_color" to entity "main-entity" on plugin "event-rgb-02" device "main-device"
    Then the command state is "pending"
    When plugin "event-rgb-02" ingests event {"type":"set_color","on":true,"r":10,"g":20,"b":30} for entity "main-entity" correlated with the last command
    Then entity "main-entity" on plugin "event-rgb-02" has sync_status "synced"
    And entity "main-entity" on plugin "event-rgb-02" has state field "r" equal to "10"

  # ── Temperature Sensor ───────────────────────────────────────────────────────

  Scenario: Temperature sensor reports a reading
    When plugin "event-temp-01" ingests event {"type":"state","temperature":21.5,"unit":"C"} for entity "main-entity"
    Then entity "main-entity" on plugin "event-temp-01" has sync_status "synced"
    And entity "main-entity" on plugin "event-temp-01" has state field "temperature" equal to "21.5"
    And entity "main-entity" on plugin "event-temp-01" has state field "unit" equal to "C"

  Scenario: Temperature sensor reading updates over time
    When plugin "event-temp-01" ingests event {"type":"state","temperature":21.5,"unit":"C"} for entity "main-entity"
    And plugin "event-temp-01" ingests event {"type":"state","temperature":22.1,"unit":"C"} for entity "main-entity"
    Then entity "main-entity" on plugin "event-temp-01" has state field "temperature" equal to "22.1"

  Scenario: Second temperature sensor reports independently
    When plugin "event-temp-01" ingests event {"type":"state","temperature":18.0,"unit":"C"} for entity "main-entity"
    And plugin "event-temp-02" ingests event {"type":"state","temperature":25.0,"unit":"C"} for entity "main-entity"
    Then entity "main-entity" on plugin "event-temp-01" has state field "temperature" equal to "18"
    And entity "main-entity" on plugin "event-temp-02" has state field "temperature" equal to "25"

  # ── Binary Sensor ────────────────────────────────────────────────────────────

  Scenario: Binary sensor triggers motion detection
    When plugin "event-motion-01" ingests event {"type":"triggered","detected":true} for entity "main-entity"
    Then entity "main-entity" on plugin "event-motion-01" has sync_status "synced"
    And entity "main-entity" on plugin "event-motion-01" has state field "detected" equal to "true"

  Scenario: Binary sensor clears after motion ends
    When plugin "event-motion-01" ingests event {"type":"triggered","detected":true} for entity "main-entity"
    And plugin "event-motion-01" ingests event {"type":"cleared","detected":false} for entity "main-entity"
    Then entity "main-entity" on plugin "event-motion-01" has state field "detected" equal to "false"

  Scenario: Two binary sensors operate independently
    When plugin "event-motion-01" ingests event {"type":"triggered","detected":true} for entity "main-entity"
    And plugin "event-motion-02" ingests event {"type":"cleared","detected":false} for entity "main-entity"
    Then entity "main-entity" on plugin "event-motion-01" has state field "detected" equal to "true"
    And entity "main-entity" on plugin "event-motion-02" has state field "detected" equal to "false"

  # ── HTTP + Registry visibility ───────────────────────────────────────────────

  Scenario: Entity state is readable via HTTP GET after event
    When plugin "event-switch-01" ingests event {"type":"turn_on","on":true} for entity "main-entity"
    Then the HTTP GET entity for plugin "event-switch-01" device "main-device" entity "main-entity" has sync_status "synced"
    And the HTTP GET entity effective state has field "on" equal to "true"

  Scenario: Entity state propagates to the registry aggregate
    When plugin "event-switch-02" ingests event {"type":"turn_on","on":true} for entity "main-entity"
    And registry sync delay passes
    Then the registry entity for plugin "event-switch-02" entity "main-entity" has sync_status "synced"
    And the registry entity effective state has field "on" equal to "true"

  # ── Cross-plugin event subscription ─────────────────────────────────────────

  Scenario: Plugin can subscribe and receive events from another plugin
    Given plugin "event-light-02" subscribes to entity events
    When plugin "event-light-01" ingests event {"type":"turn_on","on":true} for entity "main-entity"
    Then plugin "event-light-02" receives an event for entity "main-entity" from plugin "event-light-01"

  Scenario: All 10 plugins can observe a broadcast event
    Given all 10 event plugins subscribe to entity events
    When plugin "event-switch-01" ingests event {"type":"turn_on","on":true} for entity "main-entity"
    Then all 10 event plugins receive an event for entity "main-entity"

  Scenario: A plugin only sees events after it subscribes
    When plugin "event-switch-01" ingests event {"type":"turn_on","on":true} for entity "main-entity"
    Given plugin "event-switch-02" subscribes to entity events
    When plugin "event-switch-01" ingests event {"type":"turn_off","on":false} for entity "main-entity"
    Then plugin "event-switch-02" receives an event for entity "main-entity" from plugin "event-switch-01"

  # ── Event ingest HTTP response ───────────────────────────────────────────────

  Scenario: Event ingest response contains the updated entity
    When plugin "event-rgb-02" ingests event {"type":"set_color","on":true,"r":0,"g":200,"b":255} for entity "main-entity"
    Then the event ingest response has sync_status "synced"
    And the event ingest response effective state has field "g" equal to "200"
