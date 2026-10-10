-- Fails while any channel still uses slack, discord, or whatsapp; delete those channels first.
ALTER TABLE notification_channels
    DROP CONSTRAINT notification_channels_platform_check,
    ADD CONSTRAINT notification_channels_platform_check
        CHECK (platform IN ('telegram', 'feishu', 'wecom'));
