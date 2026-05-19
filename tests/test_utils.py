import pytest
from main import split_username_region_locale, chunks
from podimo.utils import (is_correct_email_address, token_key,
                          randomHexId, randomFlyerId, generateHeaders)


class TestSplitUsernameRegionLocale:
    """Test the username/region/locale splitting logic."""

    def test_three_parts(self):
        """Three comma-separated parts should all be returned."""
        result = split_username_region_locale("user@email.com,de,de-DE")
        assert result == ("user@email.com", "de", "de-DE")

    def test_single_part_fallback(self):
        """Single part should return Dutch defaults."""
        result = split_username_region_locale("user@email.com")
        assert result == ("user@email.com", 'nl', 'nl-NL')

    def test_two_parts(self):
        """Two parts should also fall back to defaults."""
        result = split_username_region_locale("user@email.com,de")
        assert result == ("user@email.com", 'nl', 'nl-NL')

    def test_special_characters_in_username(self):
        """URL-encoded special chars should be handled."""
        result = split_username_region_locale("user%40example.com,de,de-DE")
        assert result == ("user%40example.com", "de", "de-DE")


class TestChunks:
    """Test the chunking utility."""

    def test_even_division(self):
        items = [1, 2, 3, 4, 5, 6]
        result = list(chunks(items, 3))
        assert result == [[1, 2, 3], [4, 5, 6]]

    def test_uneven_division(self):
        items = [1, 2, 3, 4, 5]
        result = list(chunks(items, 2))
        assert result == [[1, 2], [3, 4], [5]]

    def test_empty_list(self):
        result = list(chunks([], 5))
        assert result == []

    def test_single_item(self):
        result = list(chunks([1], 5))
        assert result == [[1]]

    def test_chunk_size_one(self):
        items = [1, 2, 3]
        result = list(chunks(items, 1))
        assert result == [[1], [2], [3]]


class TestIsCorrectEmailAddress:
    """Test email validation."""

    def test_valid_email(self):
        assert is_correct_email_address("user@example.com")

    def test_valid_email_with_plus(self):
        assert is_correct_email_address("user+tag@example.com")

    def test_invalid_no_at(self):
        assert not is_correct_email_address("not-an-email")

    def test_invalid_empty(self):
        assert not is_correct_email_address("")

    def test_invalid_no_domain(self):
        assert not is_correct_email_address("user@")

    def test_invalid_no_user(self):
        assert not is_correct_email_address("@example.com")

    def test_common_facebook_apple_format(self):
        """Podimo supports Sign in with Apple / Facebook emails."""
        assert is_correct_email_address("abc123@privaterelay.appleid.com")


class TestTokenKey:
    """Test token key generation."""

    def test_deterministic(self):
        k1 = token_key("a@b.com", "pw")
        k2 = token_key("a@b.com", "pw")
        assert k1 == k2
        assert len(k1) == 64  # SHA-256 hex

    def test_different_passwords(self):
        k1 = token_key("a@b.com", "pw1")
        k2 = token_key("a@b.com", "pw2")
        assert k1 != k2

    def test_different_usernames(self):
        k1 = token_key("a@b.com", "pw")
        k2 = token_key("c@d.com", "pw")
        assert k1 != k2

    def test_unicode_handling(self):
        """Should handle unicode characters in credentials."""
        k = token_key("tëst@émáil.com", "pässwörd")
        assert len(k) == 64


class TestRandomHexId:
    """Test random ID generation."""

    def test_length(self):
        rid = randomHexId(16)
        assert len(rid) == 16

    def test_hex_characters_only(self):
        rid = randomHexId(32)
        assert all(c in "0123456789abcdef" for c in rid)

    def test_different_calls(self):
        rid1 = randomHexId(16)
        rid2 = randomHexId(16)
        assert rid1 != rid2  # Technically possible to collide, extremely unlikely

    def test_zero_length(self):
        assert randomHexId(0) == ""


class TestRandomFlyerId:
    """Test random Flyer ID generation."""

    def test_format(self):
        fid = randomFlyerId()
        parts = fid.split("-")
        assert len(parts) == 2
        assert len(parts[0]) == 13
        assert len(parts[1]) == 13
        assert parts[0].isdigit()
        assert parts[1].isdigit()

    def test_different_calls(self):
        fid1 = randomFlyerId()
        fid2 = randomFlyerId()
        assert fid1 != fid2


class TestGenerateHeaders:
    """Test Android app header generation."""

    def test_without_authorization(self):
        headers = generateHeaders(None, "nl-NL")
        assert headers["user-os"] == "android"
        assert headers["user-agent"] == "Podimo/2.45.1 build 566/Android 33"
        assert headers["user-version"] == "2.45.1"
        assert headers["user-locale"] == "nl-NL"
        assert "user-unique-id" in headers
        assert "authorization" not in headers

    def test_with_authorization(self):
        headers = generateHeaders("Bearer token123", "de-DE")
        assert headers["user-locale"] == "de-DE"
        assert headers["authorization"] == "Bearer token123"

    def test_unique_id_randomness(self):
        h1 = generateHeaders(None, "nl-NL")
        h2 = generateHeaders(None, "nl-NL")
        assert h1["user-unique-id"] != h2["user-unique-id"]
        assert len(h1["user-unique-id"]) == 16
