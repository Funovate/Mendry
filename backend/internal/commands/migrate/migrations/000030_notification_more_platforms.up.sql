-- Additive: allow Slack, Discord, and WhatsApp notification channels.
ALTER TABLE notification_channels
    DROP CONSTRAINT notification_channels_platform_check,
    ADD CONSTRAINT notification_channels_platform_check
        CHECK (platform IN ('telegram', 'feishu', 'wecom', 'slack', 'discord', 'whatsapp'));
