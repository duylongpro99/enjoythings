description: End the active drill, restore the target, and write the debrief

As the Executor (`drills/roles/executor.md`):

1. Run `drills/bin/drill end` (requires state DEBRIEFED; use
   `drills/bin/drill abort` to stop without a score). It reverts the fault,
   stops load, resets the target, removes the run's build tree, and writes
   `debrief.md` with the timeline, the fault, the reference solution, and every
   proposal. For a code fault the debrief also reveals the sealed fault patch
   ("What the fault changed") and the engineer's own diff against the starting
   commit ("Your fix vs the reference").
2. Open the run's `debrief.md` and complete the rubric section: one paragraph
   per dimension, citing timeline entries and the metrics you observed. Grade
   articulation of the trade-off, not just whether the probe passed.
3. Summarise the debrief to the engineer in under ten sentences. Lead with what
   the fault was and what the first useful signal would have been.
4. Runs are committed. Stage `drills/runs/<run>/` so progress is queryable.
