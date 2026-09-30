-- file_checks held one guardrail's verdict on one file's content, so a file-guard
-- could skip content it had already passed and be re-fired on one it had
-- refused. File-guards judge commits now, and their verdicts live in the check
-- results (internal/checkstore), so nothing reads or writes it.
DROP TABLE IF EXISTS file_checks;
