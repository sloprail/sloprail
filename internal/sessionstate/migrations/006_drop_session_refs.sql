-- Session range tracking is gone: file-guards are judged on an explicit base..head, so a
-- session no longer records the refs it committed on. (The folders it worked in stay: the
-- rules of each folder apply there.)
DROP TABLE IF EXISTS session_refs;
