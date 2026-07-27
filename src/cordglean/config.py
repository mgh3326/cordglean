from __future__ import annotations

import os
from dataclasses import dataclass

DEFAULT_API_BASE_URL = "https://discord.com/api/v10"


class ConfigurationError(RuntimeError):
    """Raised when required server configuration is missing or invalid."""


def validate_snowflake(value: str, label: str) -> str:
    """Validate an untrusted Discord snowflake before using it in an API path."""
    if not value or not value.isascii() or not value.isdigit():
        raise ValueError(f"{label} must be a Discord snowflake containing only digits")
    return value


@dataclass(frozen=True)
class ChannelAccessPolicy:
    """Defense-in-depth hook layered on Discord's server-side bot permissions."""

    allowed_channel_ids: frozenset[str] | None = None

    @classmethod
    def from_environment(cls) -> ChannelAccessPolicy:
        raw = os.environ.get("DISCORD_ALLOWED_CHANNEL_IDS", "").strip()
        if not raw:
            return cls()
        ids = frozenset(
            validate_snowflake(item.strip(), "DISCORD_ALLOWED_CHANNEL_IDS entry")
            for item in raw.split(",")
            if item.strip()
        )
        return cls(allowed_channel_ids=ids)

    def require_channel(self, channel_id: str) -> None:
        if self.allowed_channel_ids is not None and channel_id not in self.allowed_channel_ids:
            raise PermissionError(f"channel {channel_id} is not in DISCORD_ALLOWED_CHANNEL_IDS")

    def permits_channel(self, channel_id: str) -> bool:
        return self.allowed_channel_ids is None or channel_id in self.allowed_channel_ids


@dataclass(frozen=True)
class Settings:
    token: str
    api_base_url: str
    channel_policy: ChannelAccessPolicy

    @classmethod
    def from_environment(cls) -> Settings:
        token = os.environ.get("DISCORD_BOT_TOKEN", "")
        if not token:
            raise ConfigurationError(
                "DISCORD_BOT_TOKEN is required in the cordglean process environment"
            )
        base_url = os.environ.get("DISCORD_API_BASE_URL", DEFAULT_API_BASE_URL).rstrip("/")
        if not base_url.startswith(("https://", "http://")):
            raise ConfigurationError("DISCORD_API_BASE_URL must be an HTTP(S) URL")
        return cls(
            token=token,
            api_base_url=base_url,
            channel_policy=ChannelAccessPolicy.from_environment(),
        )
