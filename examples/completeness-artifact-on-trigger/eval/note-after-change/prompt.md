greet() in src/greeter.py always appends "!" even for an empty name, which
looks wrong ("Hello, !"). Fix it so an empty name greets generically
instead (e.g. "Hello there!").
