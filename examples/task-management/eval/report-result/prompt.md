Pick up the rate-limit task in memories/tasks/bugfix/rate-limit-window. Add
burst limits to what that task asks for: a short spike of requests shouldn't
trip the limiter. I only have time for the rolling-window fix today, so do that
part, and keep the task's files up to date.
