import pytest

from cordglean.config import ChannelAccessPolicy, validate_snowflake


def test_validate_snowflake_rejects_path_content() -> None:
    with pytest.raises(ValueError):
        validate_snowflake("../messages", "channel_id")


def test_channel_policy_is_open_when_unset() -> None:
    assert ChannelAccessPolicy().permits_channel("123")


def test_channel_policy_denies_unlisted_channel() -> None:
    policy = ChannelAccessPolicy(frozenset({"123"}))
    with pytest.raises(PermissionError):
        policy.require_channel("456")
