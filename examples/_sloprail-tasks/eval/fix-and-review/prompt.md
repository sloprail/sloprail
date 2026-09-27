slugify() in src/slug.py drops digits: "Release 2 notes" comes out as
"release-notes" instead of "release-2-notes". Track this as a task first,
then fix it.
