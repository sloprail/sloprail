-- Session range tracking is gone: file-guards are judged on an explicit base..head, so a
-- session no longer records the folders it worked in or the refs it committed on.
DROP TABLE IF EXISTS session_refs;
DROP TABLE IF EXISTS session_folders;
