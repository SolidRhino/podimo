import pytest
from main import extract_audio_url, podcastsToRss


class TestExtractAudioUrl:
    """Test audio URL extraction from episode data."""

    def test_audio_field_present(self):
        episode = {
            "audio": {"url": "https://cdn.podimo.com/audio/ep.mp3", "duration": 3600},
            "streamMedia": {"url": "", "duration": 0}
        }
        url, duration = extract_audio_url(episode)
        assert url == "https://cdn.podimo.com/audio/ep.mp3"
        assert duration == 3600

    def test_audio_null_fallback_to_streammedia(self):
        episode = {
            "audio": None,
            "streamMedia": {"url": "https://cdn.podimo.com/stream/ep.m3u8", "duration": 1800}
        }
        url, duration = extract_audio_url(episode)
        assert url == "https://cdn.podimo.com/stream/ep.m3u8"
        assert duration == 1800

    def test_audio_empty_fallback_to_streammedia(self):
        episode = {
            "audio": {"url": "", "duration": 0},
            "streamMedia": {"url": "https://cdn.podimo.com/stream/ep.m3u8", "duration": 1800}
        }
        url, duration = extract_audio_url(episode)
        assert url == "https://cdn.podimo.com/stream/ep.m3u8"
        assert duration == 1800

    def test_hls_to_mp3_conversion(self):
        episode = {
            "audio": None,
            "streamMedia": {"url": "https://cdn.podimo.com/hls-media/ep123/main.m3u8", "duration": 2400}
        }
        url, duration = extract_audio_url(episode)
        assert url == "https://cdn.podimo.com/audios/ep123.mp3"
        assert duration == 2400

    def test_no_audio_at_all(self):
        episode = {
            "audio": None,
            "streamMedia": None
        }
        url, duration = extract_audio_url(episode)
        assert url is None
        assert duration == 0


class TestPodcastsToRss:
    """Test RSS feed generation."""

    @pytest.mark.asyncio
    async def test_feed_with_episodes(self, mock_podcast_data):
        feed = await podcastsToRss("12345-1234-1234-1234-123456789abc", mock_podcast_data, "en-US")
        assert b"Test Podcast" in feed
        assert b"A test podcast description" in feed
        assert b"Test Author" in feed
        assert b"Episode 1" in feed
        assert b"https://cdn.podimo.com/audio/ep-001.mp3" in feed
        assert b"&lt;itunes:block&gt;Yes&lt;/itunes:block&gt;" in feed or b"itunes_block" in feed

    @pytest.mark.asyncio
    async def test_feed_without_episodes(self, mock_podcast_no_episodes):
        """BUG FIX: Previously generated malformed RSS for empty episode lists."""
        feed = await podcastsToRss("67890-1234-1234-1234-123456789abc", mock_podcast_no_episodes, "nl-NL")
        assert b"Empty Podcast" in feed
        assert b"No episodes here" in feed
        assert b"Empty Author" in feed
        # Should still be valid XML with channel metadata
        assert b"&lt;channel&gt;" in feed or b"<channel>" in feed

    @pytest.mark.asyncio
    async def test_public_feeds_no_block(self, mock_podcast_data, monkeypatch):
        import podimo.config as cfg
        monkeypatch.setattr(cfg, "PUBLIC_FEEDS", True)
        feed = await podcastsToRss("12345-1234-1234-1234-123456789abc", mock_podcast_data, "en-US")
        # Should NOT contain itunes_block when public
        assert b"itunes_block" not in feed.lower()
