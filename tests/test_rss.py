import pytest
from unittest.mock import AsyncMock, MagicMock, patch
from main import extract_audio_url, podcastsToRss, addFeedEntry, urlHeadInfo


def _make_head_response_mock(headers):
    """Create a mock response object for session.head()."""
    response = MagicMock()
    response.headers = headers
    return response


def _make_session_mock(response_headers):
    """Create a mock aiohttp ClientSession that supports async with."""
    response = _make_head_response_mock(response_headers)
    head_cm = AsyncMock()
    head_cm.__aenter__ = AsyncMock(return_value=response)
    head_cm.__aexit__ = AsyncMock(return_value=False)

    session = MagicMock()
    session.head.return_value = head_cm
    return session


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


class TestUrlHeadInfo:
    """Test HEAD request for audio file metadata."""

    @pytest.mark.asyncio
    async def test_cached_entry(self):
        """Should return cached value immediately."""
        import podimo.cache as cache
        cache.insertIntoHeadCache("test-id", "1024", "audio/mpeg")

        mock_session = MagicMock()
        result = await urlHeadInfo(mock_session, "test-id", "https://example.com/audio.mp3", "nl-NL")
        assert result == ("1024", "audio/mpeg")
        # Session should NOT be used when cache hit
        mock_session.head.assert_not_called()

    @pytest.mark.asyncio
    async def test_head_request_success(self):
        """Should make HEAD request when not cached."""
        # Clear cache first
        import podimo.cache as cache
        if "test-head-id" in cache.head_cache:
            del cache.head_cache["test-head-id"]

        mock_session = _make_session_mock({'content-length': '2048'})

        result = await urlHeadInfo(mock_session, "test-head-id", "https://example.com/audio.mp3", "nl-NL")
        assert result == ("2048", "audio/mpeg")

        # Verify it was cached
        cached = cache.getHeadEntry("test-head-id")
        assert cached == ("2048", "audio/mpeg")

    @pytest.mark.asyncio
    async def test_guess_type_fallback(self):
        """Should use response Content-Type when guess_type returns None."""
        import podimo.cache as cache
        if "test-head-id2" in cache.head_cache:
            del cache.head_cache["test-head-id2"]

        mock_session = _make_session_mock({'content-length': '4096', 'content-type': 'audio/x-m4a'})

        # URL without extension so guess_type returns None
        result = await urlHeadInfo(mock_session, "test-head-id2", "https://example.com/audio-noext", "nl-NL")
        assert result == ("4096", "audio/x-m4a")

    @pytest.mark.asyncio
    async def test_connection_error_falls_back_without_caching(self):
        """Should fall back to defaults without caching on aiohttp.ClientError.

        Regression: previously only asyncio.TimeoutError was retried; a
        ClientError (connection reset, DNS failure) propagated and killed the
        whole feed build. Now it retries, then returns ('0','audio/mpeg')
        without inserting into head_cache (so the next request can retry).
        """
        import podimo.cache as cache
        from aiohttp import ClientError
        from unittest.mock import patch, AsyncMock

        ep_id = "test-head-conn-err"
        if ep_id in cache.head_cache:
            del cache.head_cache[ep_id]

        mock_session = MagicMock()
        mock_session.head = MagicMock(side_effect=ClientError("connection reset"))

        with patch("main.asyncio.sleep", new=AsyncMock()) as mock_sleep:
            result = await urlHeadInfo(mock_session, ep_id, "https://example.com/audio.mp3", "nl-NL")

        assert result == ("0", "audio/mpeg")
        # Retried 3 times total (2 sleeps between retries)
        assert mock_session.head.call_count == 3
        # Must NOT cache the transient failure
        assert cache.getHeadEntry(ep_id) is None


class TestAddFeedEntry:
    """Test RSS entry generation for a single episode."""

    @pytest.mark.asyncio
    async def test_basic_entry(self, mock_podcast_data, monkeypatch):
        """Should add a basic feed entry without video."""
        import main
        monkeypatch.setattr(main, "VIDEO_ENABLED", False)

        from feedgen.feed import FeedGenerator
        fg = FeedGenerator()
        fg.load_extension("podcast")

        mock_session = _make_session_mock({'content-length': '1024'})

        episode = mock_podcast_data["episodes"][0]
        image_urls: list[str] = []
        await addFeedEntry(fg, episode, mock_session, "nl-NL", image_urls)

        entries = fg.entry()
        assert len(entries) == 1
        assert entries[0].title() == "Episode 1"
        assert entries[0].description() == "Episode 1 description"

    @pytest.mark.asyncio
    async def test_entry_with_video_check(self, mock_podcast_data, monkeypatch):
        """Should append video URL to description when video exists."""
        import main
        monkeypatch.setattr(main, "VIDEO_ENABLED", True)
        monkeypatch.setattr(main, "VIDEO_CHECK_ENABLED", True)
        monkeypatch.setattr(main, "VIDEO_TITLE_SUFFIX", " [VIDEO]")

        from feedgen.feed import FeedGenerator
        fg = FeedGenerator()
        fg.load_extension("podcast")

        mock_session = _make_session_mock({'content-length': '1024'})

        episode = mock_podcast_data["episodes"][0]
        image_urls: list[str] = []
        # Mock video_exists_at_url to return True
        with patch.object(main, 'video_exists_at_url', new=AsyncMock(return_value=True)):
            await addFeedEntry(fg, episode, mock_session, "nl-NL", image_urls)

        entries = fg.entry()
        assert "Video URL found at:" in entries[0].description()
        assert entries[0].title() == "Episode 1 [VIDEO]"

    @pytest.mark.asyncio
    async def test_entry_no_audio(self):
        """Should skip entry if episode has no audio URL."""
        from feedgen.feed import FeedGenerator
        fg = FeedGenerator()
        fg.load_extension("podcast")

        mock_session = MagicMock()
        episode = {
            "id": "ep-no-audio",
            "title": "No Audio",
            "description": "Nothing to play",
            "audio": None,
            "streamMedia": None
        }

        image_urls: list[str] = []
        await addFeedEntry(fg, episode, mock_session, "nl-NL", image_urls)
        assert len(fg.entry()) == 0


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
        assert b"<itunes:block>yes</itunes:block>" in feed or b"itunes_block" in feed

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
        import main
        monkeypatch.setattr(main, "PUBLIC_FEEDS", True)
        feed = await podcastsToRss("12345-1234-1234-1234-123456789abc", mock_podcast_data, "en-US")
        # Should NOT contain itunes_block when public
        assert b"itunes_block" not in feed.lower()
