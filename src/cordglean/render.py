from __future__ import annotations

from typing import Any


def _copy_mapping(value: Any) -> dict[str, Any] | None:
    return dict(value) if isinstance(value, dict) else None


def render_embed(embed: dict[str, Any], index: int = 1) -> dict[str, Any]:
    """Return a lossless structured view plus a complete readable text rendering."""
    fields = [
        {
            "name": field.get("name", ""),
            "value": field.get("value", ""),
            "inline": bool(field.get("inline", False)),
        }
        for field in embed.get("fields", [])
        if isinstance(field, dict)
    ]
    footer = _copy_mapping(embed.get("footer"))
    author = _copy_mapping(embed.get("author"))
    provider = _copy_mapping(embed.get("provider"))
    thumbnail = _copy_mapping(embed.get("thumbnail"))
    image = _copy_mapping(embed.get("image"))
    video = _copy_mapping(embed.get("video"))

    lines = [f"[Embed {index}]"]
    if embed.get("title"):
        lines.append(f"Title: {embed['title']}")
    if embed.get("description"):
        lines.append(str(embed["description"]))
    if embed.get("url"):
        lines.append(f"URL: {embed['url']}")
    if author:
        author_line = f"Author: {author.get('name', '')}"
        if author.get("url"):
            author_line += f" ({author['url']})"
        lines.append(author_line)
    for position, field in enumerate(fields, start=1):
        inline = " inline" if field["inline"] else ""
        lines.append(f"Field {position}{inline} — {field['name']}:")
        lines.append(str(field["value"]))
    if footer:
        lines.append(f"Footer: {footer.get('text', '')}")
    if embed.get("timestamp"):
        lines.append(f"Timestamp: {embed['timestamp']}")
    if provider and provider.get("name"):
        lines.append(f"Provider: {provider['name']}")
    if thumbnail and thumbnail.get("url"):
        lines.append(f"Thumbnail: {thumbnail['url']}")
    if image and image.get("url"):
        lines.append(f"Image: {image['url']}")
    if video and video.get("url"):
        lines.append(f"Video: {video['url']}")

    return {
        "text": "\n".join(lines),
        "title": embed.get("title"),
        "description": embed.get("description"),
        "url": embed.get("url"),
        "type": embed.get("type"),
        "color": embed.get("color"),
        "flags": embed.get("flags"),
        "timestamp": embed.get("timestamp"),
        "fields": fields,
        "footer": footer,
        "author": author,
        "provider": provider,
        "thumbnail": thumbnail,
        "image": image,
        "video": video,
    }


def render_attachment(attachment: dict[str, Any]) -> dict[str, Any]:
    """Preserve Discord attachment metadata without downloading the payload."""
    known_keys = (
        "id",
        "filename",
        "title",
        "description",
        "content_type",
        "size",
        "url",
        "proxy_url",
        "height",
        "width",
        "placeholder",
        "placeholder_version",
        "ephemeral",
        "duration_secs",
        "waveform",
        "flags",
        "clip_participants",
        "clip_created_at",
        "application",
    )
    result = {key: attachment.get(key) for key in known_keys if key in attachment}
    result["raw"] = dict(attachment)
    return result


def render_message(
    message: dict[str, Any],
    *,
    include_raw_embeds: bool = True,
) -> dict[str, Any]:
    embeds = [
        embed for embed in message.get("embeds", []) if isinstance(embed, dict)
    ]
    rendered_embeds = [
        render_embed(embed, index) for index, embed in enumerate(embeds, start=1)
    ]
    attachments = [
        render_attachment(attachment)
        for attachment in message.get("attachments", [])
        if isinstance(attachment, dict)
    ]

    display_parts: list[str] = []
    if message.get("content"):
        display_parts.append(str(message["content"]))
    display_parts.extend(embed["text"] for embed in rendered_embeds)
    for attachment in attachments:
        label = attachment.get("filename", attachment.get("id", "attachment"))
        detail = f"[Attachment] {label}"
        if attachment.get("content_type"):
            detail += f" ({attachment['content_type']})"
        if attachment.get("size") is not None:
            detail += f", {attachment['size']} bytes"
        if attachment.get("url"):
            detail += f"\n{attachment['url']}"
        display_parts.append(detail)

    author = message.get("author")
    result: dict[str, Any] = {
        "id": message.get("id"),
        "channel_id": message.get("channel_id"),
        "guild_id": message.get("guild_id"),
        "type": message.get("type"),
        "content": message.get("content", ""),
        "display_text": "\n\n".join(display_parts),
        "timestamp": message.get("timestamp"),
        "edited_timestamp": message.get("edited_timestamp"),
        "author": dict(author) if isinstance(author, dict) else author,
        "webhook_id": message.get("webhook_id"),
        "pinned": message.get("pinned", False),
        "tts": message.get("tts", False),
        "mention_everyone": message.get("mention_everyone", False),
        "mentions": message.get("mentions", []),
        "mention_roles": message.get("mention_roles", []),
        "message_reference": message.get("message_reference"),
        "flags": message.get("flags"),
        "rendered_embeds": rendered_embeds,
        "attachments": attachments,
    }
    if include_raw_embeds:
        result["embeds"] = embeds
    return result
