Let's pick the rate-limit work back up. I also want burst limits as part of it:
a short spike of requests shouldn't trip the limiter. I only have time for the
rolling-window fix today, so just do that part for now.
