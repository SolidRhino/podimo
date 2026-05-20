import pytest
from unittest.mock import MagicMock, AsyncMock

@pytest.fixture(autouse=True)
def reset_rate_limiter():
    """Reset the rate limiter state between tests."""
    import main
    main.proactive.clear()

@pytest.fixture
def mock_podcast_data():
    """Sample GraphQL podcast response with episodes."""
    return {
        "podcast": {
            "title": "Test Podcast",
            "description": "A test podcast description",
            "webAddress": "https://podimo.com/shows/12345",
            "authorName": "Test Author",
            "language": "en-US",
            "images": {
                "coverImageUrl": "https://cdn.podimo.com/images/12345.jpg"
            }
        },
        "episodes": [
            {
                "id": "ep-001",
                "artist": "Test Artist",
                "podcastName": "Test Podcast",
                "imageUrl": "https://cdn.podimo.com/images/ep-001.jpg",
                "description": "Episode 1 description",
                "datetime": "2024-01-01T00:00:00Z",
                "publishDatetime": "2024-01-01T00:00:00Z",
                "title": "Episode 1",
                "audio": {
                    "url": "https://cdn.podimo.com/audio/ep-001.mp3",
                    "duration": 3600
                },
                "streamMedia": {
                    "url": "",
                    "duration": 0
                }
            }
        ]
    }

@pytest.fixture
def mock_podcast_no_episodes():
    """Sample GraphQL podcast response with zero episodes."""
    return {
        "podcast": {
            "title": "Empty Podcast",
            "description": "No episodes here",
            "webAddress": "https://podimo.com/shows/67890",
            "authorName": "Empty Author",
            "language": "nl-NL",
            "images": {
                "coverImageUrl": "https://cdn.podimo.com/images/67890.jpg"
            }
        },
        "episodes": []
    }

@pytest.fixture
def mock_client():
    """Mocked PodimoClient with a fake token."""
    from podimo.client import PodimoClient
    client = PodimoClient("test@example.com", "password123", "nl", "nl-NL")
    client.token = "fake-token-12345"
    return client
