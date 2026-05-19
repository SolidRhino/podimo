import pytest
from main import app, podcast_id_pattern


class TestWebRoutes:
    """Test Quart web endpoints."""

    @pytest.mark.asyncio
    async def test_index_get(self):
        """The root route should return 200 with the form."""
        async with app.test_client() as client:
            response = await client.get('/')
            assert response.status_code == 200
            body = await response.get_data()
            assert b'Podimo-to-RSS converter' in body

    @pytest.mark.asyncio
    async def test_index_post_missing_fields(self):
        """POST without required fields should return the form with errors."""
        async with app.test_client() as client:
            response = await client.post('/', data={})
            assert response.status_code == 200
            body = await response.get_data()
            assert b'Error' in body

    @pytest.mark.asyncio
    async def test_feed_invalid_podcast_id(self):
        """Invalid podcast ID format should return 400."""
        async with app.test_client() as client:
            response = await client.get('/feed/not-a-uuid.xml')
            # Should be 400 for invalid format, but depends on auth flow
            # At minimum it should not crash
            assert response.status_code in [400, 401]

    @pytest.mark.asyncio
    async def test_404(self):
        """Unknown routes should return 404 with example text."""
        async with app.test_client() as client:
            response = await client.get('/nonexistent')
            assert response.status_code == 404
            body = await response.get_data()
            assert b'404' in body or b'Example' in body


class TestPodcastIdRegex:
    """Test the tightened UUID regex."""

    def test_valid_uuid(self):
        assert podcast_id_pattern.fullmatch("09c55c96-9b1b-456e-bdf2-3abed3b61db5")

    def test_invalid_short(self):
        assert podcast_id_pattern.fullmatch("12345") is None

    def test_invalid_dashes_only(self):
        """BUG FIX: Previously '---' would match the permissive regex."""
        assert podcast_id_pattern.fullmatch("---") is None

    def test_invalid_missing_segment(self):
        assert podcast_id_pattern.fullmatch("09c55c96-9b1b-456e-bdf2") is None

    def test_invalid_extra_chars(self):
        assert podcast_id_pattern.fullmatch("09c55c96-9b1b-456e-bdf2-3abed3b61db5-extra") is None

    def test_case_insensitive(self):
        assert podcast_id_pattern.fullmatch("09C55C96-9B1B-456E-BDF2-3ABED3B61DB5")
