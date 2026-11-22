-- Register of the servers, version 1.
--
-- Several servers that run TimeVault can share one PostgreSQL database, each in a schema of its own (docs/adr/0019-*.md).
-- This file is not one of the migrations of a server: it is applied in the schema timevault_cluster, by
-- "timevault-server cluster register", and the servers only read it and report in it that they are alive.
--
-- servers holds one row for each server of the group:
--   name                 how the server is called in addresses and on the pages: short, lower case, unique;
--   schema               the schema that holds its tables (unique, a server never shares a schema);
--   version              the version of the program that last reported;
--   contract             the number of the read contract that program speaks (internal/cluster.Contract). Another server
--                        reads this one only when it knows that number; 0 means it has not reported yet;
--   repository_id        the id of its repository;
--   repository_root,
--   restore_root,
--   restore_display_root the folders of its repository and of its restores on its disk, and the network path people reach the
--                        restore folder by. They are there to hide the real folders in the texts that another server shows
--                        for it, and only administrators are shown them;
--   registered_at/_by    when and by whom the server was registered;
--   last_seen            when the server last reported, by the clock of the database. A server that has not reported for a
--                        couple of minutes is shown as offline; its history can still be read.

CREATE TABLE servers (
    name                 text        PRIMARY KEY CHECK (name ~ '^[a-z0-9][a-z0-9_-]{0,31}$'),
    schema               text        NOT NULL CHECK (schema ~ '^[a-z_][a-z0-9_]{0,62}$'),
    version              text        NOT NULL DEFAULT '',
    contract             integer     NOT NULL DEFAULT 0 CHECK (contract >= 0),
    repository_id        text        NOT NULL DEFAULT '',
    repository_root      text        NOT NULL DEFAULT '',
    restore_root         text        NOT NULL DEFAULT '',
    restore_display_root text        NOT NULL DEFAULT '',
    registered_at        timestamptz NOT NULL DEFAULT now(),
    registered_by        text        NOT NULL DEFAULT '',
    last_seen            timestamptz,
    CONSTRAINT servers_schema_uq UNIQUE (schema)
);
