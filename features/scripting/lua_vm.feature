Feature: Lua VM lifecycle
  Proves that LuaVM correctly handles startup, errors, and shutdown.

  Background:
    Given a scripting environment with entity "vm-test-entity" of domain "switch"

  Scenario: OnInit is called exactly once at VM start
    Given a Lua script:
      """
      CallCount = 0
      function OnInit(ctx)
        CallCount = CallCount + 1
      end
      """
    When I start the Lua VM
    Then the VM starts without error
    And the Lua global "CallCount" equals 1

  Scenario: VM with no OnInit function starts successfully
    Given a Lua script:
      """
      x = 42
      """
    When I start the Lua VM
    Then the VM starts without error

  Scenario: Syntax error in Lua script fails at start
    Given a Lua script:
      """
      function OnInit(ctx
        -- missing closing paren
      end
      """
    When I start the Lua VM
    Then the VM fails to start with an error

  Scenario: Runtime error in OnInit fails the VM
    Given a Lua script:
      """
      function OnInit(ctx)
        error("deliberate failure")
      end
      """
    When I start the Lua VM
    Then the VM fails to start with an error

  Scenario: Stopping a running VM is clean
    Given a Lua script:
      """
      function OnInit(ctx)
      end
      """
    When I start the Lua VM
    Then the VM starts without error
    When I stop the Lua VM
    Then the VM is stopped

  Scenario: Multiple VMs can run independently
    Given a Lua script:
      """
      InstanceID = 0
      function OnInit(ctx)
        InstanceID = InstanceID + 1
      end
      """
    When I start 3 Lua VMs for 3 different entities
    Then all 3 VMs start without error
    And each VM has Lua global "InstanceID" equal to 1
