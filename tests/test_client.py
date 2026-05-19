import pytest
from podimo.client import PodimoClient
from podimo.utils import token_key


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
