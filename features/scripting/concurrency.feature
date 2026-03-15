Feature: Scripting Concurrency and Contention
  Ensure the Gateway remains stable and consistent when multiple scripts
  or API calls attempt to modify the same resources simultaneously.

  Background:
    Given a scripting event environment with test entities

  Scenario: Multiple Lua VMs thrashing labels on the same entity
    Given a Lua script:
      """
      function OnInit(ctx)
        -- Each VM attempts to add its own unique label concurrently
        local labelName = "VM-" .. math.random(1, 10000)
        
        for i = 1, 5 do
          local labels = This.GetLabels() or {}
          labels["test-concurrency"] = labels["test-concurrency"] or {}
          table.insert(labels["test-concurrency"], labelName .. "-" .. i)
          
          -- Trigger a registry update
          This.SendEvent({ labels = labels })
        end
      end
      """
    And 10 Lua VMs for entity "entity-a"
    When all 10 VMs start simultaneously
    And I wait 2 seconds
    Then the Gateway should not have panicked
    # We expect some loss here until atomic labels are implemented, 
    # but we want to verify at least SOME updates made it.
    And the entity "entity-a" should have at least 5 values in the "test-concurrency" label

  Scenario: High-frequency state updates from many scripts
    Given a Lua script:
      """
      function OnInit(ctx)
        TimerService.Scripting.Every(0.05, function()
          -- Blast the registry with state updates
          -- We include the type so the test harness can count it
          This.SendEvent({ 
            type = "entity.original.statechange",
            power = true, 
            tick = math.random(1, 1000)
          })
        end)
      end
      """
    And 5 Lua VMs for 5 different entities
    When all 5 VMs start simultaneously
    And I wait 1 seconds
    Then the recorded event count for "entity.original.statechange" is at least 50
    And the Gateway should remain healthy
