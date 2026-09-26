# billing-api

Internal billing API. Express + Prisma.

## Layout

- `src/endpoints/<verb>-<resource>.ts` — one Express router per endpoint
- `src/app.ts` — mounts every endpoint router
- `test/endpoints/<verb>-<resource>.test.ts` — one test file per endpoint
- `prisma/schema.prisma` — the data model

Run `npm test` before pushing.
