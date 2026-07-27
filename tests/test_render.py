from cordglean.render import render_message


def test_embed_only_message_is_fully_readable_and_lossless() -> None:
    raw_embed = {
        "title": "Pipeline result freshness breach",
        "description": "problem_keys: news-relevance, collector:duplicates-spike",
        "url": "https://example.invalid/run/42",
        "type": "rich",
        "color": 15158332,
        "timestamp": "2026-07-26T23:14:00+00:00",
        "fields": [
            {
                "name": "A · News freshness",
                "value": "pending=3304\nstaleness=9h 6m",
                "inline": False,
            },
            {
                "name": "C · Collector",
                "value": "WARNING",
                "inline": True,
            },
        ],
        "footer": {
            "text": "Ninetales",
            "icon_url": "https://example.invalid/footer.png",
        },
        "author": {
            "name": "Alert Bot",
            "url": "https://example.invalid/bot",
            "icon_url": "https://example.invalid/author.png",
        },
        "thumbnail": {"url": "https://example.invalid/thumb.png"},
        "image": {"url": "https://example.invalid/image.png"},
        "provider": {"name": "monitor"},
        "flags": 32,
        "future_discord_key": {"must": "survive in raw"},
    }
    message = {
        "id": "1531077181428596941",
        "channel_id": "1497292797470965943",
        "content": "",
        "timestamp": "2026-07-26T23:14:00+00:00",
        "author": {"id": "1", "username": "Ninetales", "bot": True},
        "embeds": [raw_embed],
        "attachments": [
            {
                "id": "2",
                "filename": "report.json",
                "content_type": "application/json",
                "size": 42,
                "url": "https://cdn.example.invalid/report.json",
                "proxy_url": "https://proxy.example.invalid/report.json",
                "description": "full result",
                "placeholder": "abc123",
                "placeholder_version": 1,
                "future_attachment_key": "preserved",
            }
        ],
    }

    rendered = render_message(message)
    text = rendered["display_text"]

    assert "Pipeline result freshness breach" in text
    assert "problem_keys: news-relevance" in text
    assert "A · News freshness" in text
    assert "pending=3304" in text
    assert "C · Collector" in text
    assert "Footer: Ninetales" in text
    assert "Timestamp: 2026-07-26T23:14:00+00:00" in text
    assert "[Attachment] report.json (application/json), 42 bytes" in text
    assert rendered["rendered_embeds"][0]["fields"][1]["inline"] is True
    assert rendered["rendered_embeds"][0]["footer"]["icon_url"].endswith("footer.png")
    assert rendered["rendered_embeds"][0]["flags"] == 32
    assert rendered["attachments"][0]["raw"]["future_attachment_key"] == "preserved"
    assert rendered["embeds"][0] == raw_embed


def test_get_message_can_omit_raw_embeds_without_losing_rendering() -> None:
    rendered = render_message(
        {"content": "", "embeds": [{"description": "still visible"}]},
        include_raw_embeds=False,
    )
    assert "embeds" not in rendered
    assert rendered["display_text"] == "[Embed 1]\nstill visible"
