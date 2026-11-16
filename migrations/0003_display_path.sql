-- TimeVault schema, version 3: display path of a source.
--
-- display_path is the network path people know the folder by, for example
-- \\server\share\folder, while root_path is the physical folder on the server (D:\...),
-- which ordinary users never see. NULL means the source has no display path. It can
-- be changed at any time, also when the source already has recovery points: it is only
-- shown, nothing in the history refers to it.

ALTER TABLE sources
    ADD COLUMN display_path text CHECK (display_path IS NULL OR length(display_path) BETWEEN 1 AND 520);
