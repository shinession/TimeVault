-- TimeVault schema, version 6: the Site settings.
--
-- site_settings holds the Site settings that the local administrator edits on the Site settings page: one row for
-- every setting that was given a value. A setting without a row has its default (see internal/config/site.go), and a
-- value that equals the default is not stored. key is the lower-case name of the setting, such as job_workers.
-- updated_by names the account that saved the value ('cli' when it was saved with timevault-server settings).
--
-- Nothing is copied from the .env file: the settings that used to be read from it are entered again on the page.

CREATE TABLE site_settings (
    key        text        PRIMARY KEY CHECK (key ~ '^[a-z][a-z0-9_]{0,63}$'),
    value      text        NOT NULL CHECK (value <> '' AND length(value) <= 2000),
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by text        NOT NULL DEFAULT '' CHECK (length(updated_by) <= 128)
);
