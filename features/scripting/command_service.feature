Feature: CommandService.Scripting — command dispatch API
  Proves the scripting command API constructs and routes payloads correctly
  without needing a Lua VM.

  Background:
    Given a scripting command environment with 1 plugin and 2 entities

  Scenario: Send builds payload and returns a command ID
    When I call CommandService.Scripting.Send on entity "e1" with command "turn_on" and no payload
    Then Send returns a non-empty command ID

  Scenario: Send with payload passes data through
    When I call CommandService.Scripting.Send on entity "e1" with command "set_brightness" and payload {"brightness":80}
    Then Send returns a non-empty command ID
    And the recorded command on "e1" has name "set_brightness"

  Scenario: Send to a known entity succeeds
    When I call CommandService.Scripting.Send on entity "e2" with command "turn_off" and no payload
    Then Send returns a non-empty command ID

  Scenario: Send to unknown entity returns an error
    When I call CommandService.Scripting.Send on entity "unknown-entity" with command "turn_on" and no payload
    Then Send returns an error

  Scenario: SendTo resolves entity then sends
    When I call CommandService.Scripting.SendTo with query "?domain=switch" command "turn_on"
    Then SendTo sends to at least 1 entity

  Scenario: SendTo with no matches returns entity-not-found error
    When I call CommandService.Scripting.SendTo with query "?domain=cover" command "turn_on"
    Then SendTo returns an entity-not-found error
