-- The name of the project's checkout under /home/dev, as the CLI last
-- found it on the machine (DECISIONS I-1610): "" when the machine has
-- none and work happens in the home, null until a CLI has said. The
-- dashboard's Open in Claude link opens this folder (I-1606).
alter table projects add column if not exists checkout text;
