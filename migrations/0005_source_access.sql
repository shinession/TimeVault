-- TimeVault schema, version 5: who may do what with each monitored folder.
--
-- source_access holds the grants of a folder. kind says who a grant is for: 'guest' (every visitor, signed in or
-- not; only browse), 'everyone' (every signed-in account) or 'user' (one account, named by its employee ID in
-- account, in upper case). level is browse < backup < full; each level includes the ones before it. A principal
-- appears once per folder, with the highest level it was given. A folder without rows can only be seen by
-- administrators, who have full access to every folder without being listed.
--
-- The folders that exist when this migration runs keep what everybody could do until now: guests browse, and
-- every signed-in account may also back up, verify, cancel jobs and restore.

CREATE TABLE source_access (
    source_id bigint NOT NULL REFERENCES sources (id),
    kind      text   NOT NULL CHECK (kind IN ('guest', 'everyone', 'user')),
    account   text   NOT NULL DEFAULT '' CHECK (length(account) <= 64),
    level     text   NOT NULL CHECK (level IN ('browse', 'backup', 'full')),
    PRIMARY KEY (source_id, kind, account),
    CHECK ((kind = 'user') = (account <> '')),
    CHECK (kind <> 'guest' OR level = 'browse')
);

INSERT INTO source_access (source_id, kind, level) SELECT id, 'guest', 'browse' FROM sources;
INSERT INTO source_access (source_id, kind, level) SELECT id, 'everyone', 'backup' FROM sources;
