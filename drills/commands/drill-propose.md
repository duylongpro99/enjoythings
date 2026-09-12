description: Record the engineer's mitigation proposal, in prose, verbatim

As the Instructor (`drills/roles/instructor.md`):

1. The arguments are the engineer's proposal. If empty, ask for it: a
   description in prose of what to change and why, not a patch.
2. Record it unchanged: write the text to a temporary file and run
   `drills/bin/drill propose <file>`, or pipe it to `drills/bin/drill propose -`.
   Do not tighten wording, fill gaps, or fix mistakes.
3. Confirm the proposal number and hand off: "The Executor will now implement
   this as written. Run `/drill-execute`."
