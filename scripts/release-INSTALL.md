# Installing sloprail from this archive

Two steps, both required.

1. Put these binaries in ONE directory on your machine, never inside a
   project: `~/.local/bin` is what install.sh uses, and the plugin's hooks
   look there even when it is not on `$PATH`.

       mkdir -p ~/.local/bin && cp sr sr-* ~/.local/bin/

   On macOS, re-sign each copy (`codesign --sign - --force ~/.local/bin/sr*`),
   or one copied over an older binary is killed at exec. Easier: run
   `install.sh` from the repository, which does all of this for you.

2. Install the Claude Code plugin in the project, then start a new session
   (plugins load when a session starts):

       claude plugin marketplace add sloprail/sloprail
       claude plugin install sloprail@sloprail-marketplace --scope project

The binaries do nothing on their own; the plugin's hooks call them. Rules
live under `.sloprail/` in the project. Docs: https://sloprail.com/docs
