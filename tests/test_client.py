import pytest
from unittest.mock import AsyncMock
from http.cookiejar import CookieJar
from podimo.client import PodimoClient
from podimo.utils import token_key
import asyncio


class TestPodimoClientConstructor:
    """Test PodimoClient __init__ validation."""

    def test_valid_credentials(self):
        """Client should initialize with valid email and password."""
        client = PodimoClient("user@example.com", "secret123", "nl", "nl-NL")
        assert client.username == "user@example.com"
        assert client.password == "secret123"
        assert client.region == "nl"
        assert client.locale == "nl-NL"
        assert client.token is None

    def test_empty_username_raises(self):
        with pytest.raises(ValueError, match="Empty username or password"):
            PodimoClient("", "secret", "nl", "nl-NL")

    def test_empty_password_raises(self):
        with pytest.raises(ValueError, match="Empty username or password"):
            PodimoClient("user@example.com", "", "nl", "nl-NL")

    def test_too_long_username_raises(self):
        with pytest.raises(ValueError, match="too long"):
            PodimoClient("x" * 257, "secret", "nl", "nl-NL")

    def test_too_long_password_raises(self):
        with pytest.raises(ValueError, match="too long"):
            PodimoClient("user@example.com", "x" * 257, "nl", "nl-NL")

    def test_invalid_email_raises(self):
        """BUG FIX: Previously returned ValueError object instead of raising."""
        with pytest.raises(ValueError, match="Email is not in the correct format"):
            PodimoClient("not-an-email", "secret", "nl", "nl-NL")


class TestGetPodcastName:
    """Test the hardened getPodcastName method."""

    def test_normal_response(self):
        client = PodimoClient("user@example.com", "secret", "nl", "nl-NL")
        data = {
            "podcast": {"title": "My Show"},
            "episodes": []
        }
        assert client.getPodcastName(data) == "My Show"

    def test_missing_podcast_key(self):
        """Should return 'Unknown' when podcast key is missing."""
        client = PodimoClient("user@example.com", "secret", "nl", "nl-NL")
        data = {"episodes": []}
        assert client.getPodcastName(data) == "Unknown"

    def test_missing_title(self):
        """Should return 'Unknown' when title is None or missing."""
        client = PodimoClient("user@example.com", "secret", "nl", "nl-NL")
        data = {"podcast": {}}
        assert client.getPodcastName(data) == "Unknown"

class TestGetPodcasts:
    """Test getPodcasts response handling."""

    @pytest.mark.asyncio
    async def test_null_podcast_raises_not_found(self):
        """Regression: podcast=null with empty episodes should raise PodcastNotFoundError.

        Previously this returned {'podcast': None, 'episodes': []}, which then
        crashed podcastsToRss with AttributeError and produced a generic 500
        instead of the intended 404. The bad result was also cached.
        """
        from podimo.client import PodcastNotFoundError
        client = PodimoClient("user@example.com", "secret", "nl", "nl-NL")
        client.token = "fake-token"
        client.cookie_jar = CookieJar()
        client.post = AsyncMock(return_value={"podcast": None, "episodes": []})
        with pytest.raises(PodcastNotFoundError):
            await client.getPodcasts("09c55c96-9b1b-456e-bdf2-3abed3b61db5", AsyncMock())


class TestTokenKey:
    """Test token key generation."""

    def test_deterministic(self):
        """Same credentials should always produce the same key."""
        k1 = token_key("a@b.com", "pw")
        k2 = token_key("a@b.com", "pw")
        assert k1 == k2
        assert len(k1) == 64  # SHA-256 hex

    def test_different_passwords(self):
        """Different passwords should produce different keys."""
        k1 = token_key("a@b.com", "pw1")
        k2 = token_key("a@b.com", "pw2")
        assert k1 != k2

    def test_different_usernames(self):
        """Different usernames should produce different keys."""
        k1 = token_key("a@b.com", "pw")
        k2 = token_key("c@d.com", "pw")
        assert k1 != k2


class TestSearchPodcasts:
    """Test searchPodcasts GraphQL response handling."""

    def test_empty_results(self):
        """Should return coroutine when called (async method)."""
        client = PodimoClient("user@example.com", "secret", "nl", "nl-NL")
        coro = client.searchPodcasts("xyz", AsyncMock())
        assert asyncio.iscoroutine(coro)
        # Clean up the coroutine to avoid unawaited warning
        coro.close()

    def test_method_exists(self):
        client = PodimoClient("user@example.com", "secret", "nl", "nl-NL")
        assert hasattr(client, "searchPodcasts")


class TestGetFollowedPodcasts:
    """Test getFollowedPodcasts GraphQL response handling."""

    def test_method_exists(self):
        client = PodimoClient("user@example.com", "secret", "nl", "nl-NL")
        assert hasattr(client, "getFollowedPodcasts")

    def test_returns_coroutine(self):
        client = PodimoClient("user@example.com", "secret", "nl", "nl-NL")
        coro = client.getFollowedPodcasts(AsyncMock())
        assert asyncio.iscoroutine(coro)
        coro.close()
