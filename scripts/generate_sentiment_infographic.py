#!/usr/bin/env python3
"""
Generate a nice-looking PNG infographic showing the sentiment word mapping tiers.
"""

import matplotlib.pyplot as plt
import matplotlib.patches as mpatches
from matplotlib.colors import LinearSegmentedColormap
import numpy as np

# Tier data
tiers = [
    {
        "name": "Extreme Negative",
        "range": "< 0%",
        "words": ["angry", "hostile", "grim", "miserable", "dreadful"],
        "color": "#DC2626",  # Red
        "text_color": "white"
    },
    {
        "name": "Unusually Low",
        "range": "0% - 9.5%",
        "words": ["anxious", "agitated", "irritable", "tense", "pessimistic", "cynical", "uneasy", "restless", "glum", "sullen", "somber", "weary", "subdued", "melancholy", "despondent"],
        "color": "#F97316",  # Orange
        "text_color": "white"
    },
    {
        "name": "Below Average",
        "range": "9.5% - 10.5%",
        "words": ["flat", "tired", "downbeat", "sluggish", "wary", "cautious", "skeptical", "reserved", "ambivalent", "uncertain", "distracted", "quiet", "pensive", "reflective", "solemn"],
        "color": "#FBBF24",  # Yellow
        "text_color": "black"
    },
    {
        "name": "Typical",
        "range": "10.5% - 12.5%",
        "words": ["calm", "chill", "mellow", "relaxed", "content", "peaceful", "grounded", "steady", "curious", "inquisitive", "thoughtful", "introspective", "speculative", "sentimental", "nostalgic", "playful", "mischievous", "cheeky", "ironic", "witty", "candid", "sincere", "earnest", "easygoing", "sociable", "engaged", "connected", "alert", "balanced", "settled"],
        "color": "#84CC16",  # Lime
        "text_color": "black"
    },
    {
        "name": "Above Average",
        "range": "12.5% - 14%",
        "words": ["happy", "cheerful", "upbeat", "positive", "optimistic", "hopeful", "encouraged", "pleased", "amused", "friendly", "warm", "welcoming", "lively", "supportive", "bright"],
        "color": "#22C55E",  # Green
        "text_color": "white"
    },
    {
        "name": "Unusually High",
        "range": "14% - 18%",
        "words": ["excited", "vibrant", "energetic", "enthusiastic", "inspired", "creative", "joyful", "delighted", "thrilled", "invigorated", "passionate", "spirited", "exuberant", "buoyant", "buzzing"],
        "color": "#14B8A6",  # Teal
        "text_color": "white"
    },
    {
        "name": "Extreme Positive",
        "range": ">= 18%",
        "words": ["euphoric", "ecstatic", "elated", "jubilant", "celebratory"],
        "color": "#3B82F6",  # Blue
        "text_color": "white"
    },
]

# Create figure
fig, ax = plt.subplots(figsize=(14, 10), facecolor='#1a1a2e')
ax.set_facecolor('#1a1a2e')

# Title
fig.suptitle('Bluesky Sentiment Word Mapping', fontsize=28, fontweight='bold',
             color='white', y=0.96)
ax.set_title('100 words calibrated to actual Bluesky sentiment distribution\n(Based on 116 days of historical data)',
             fontsize=14, color='#888888', pad=20)

# Remove axes
ax.axis('off')

# Layout parameters
y_start = 0.85
y_step = 0.115
box_height = 0.10
left_margin = 0.02
tier_width = 0.18
range_width = 0.10
words_start = 0.32

for i, tier in enumerate(tiers):
    y = y_start - (i * y_step)

    # Tier name box
    tier_box = mpatches.FancyBboxPatch(
        (left_margin, y - box_height/2), tier_width, box_height,
        boxstyle="round,pad=0.02,rounding_size=0.02",
        facecolor=tier['color'], edgecolor='none',
        transform=ax.transAxes
    )
    ax.add_patch(tier_box)

    # Tier name text
    ax.text(left_margin + tier_width/2, y, tier['name'],
            fontsize=11, fontweight='bold', color=tier['text_color'],
            ha='center', va='center', transform=ax.transAxes)

    # Range text
    ax.text(left_margin + tier_width + 0.02, y, tier['range'],
            fontsize=10, color='#888888', ha='left', va='center',
            transform=ax.transAxes, family='monospace')

    # Words - wrap to fit
    words = tier['words']
    if len(words) <= 5:
        word_text = ', '.join(words)
    elif len(words) <= 15:
        # Split into 2 lines
        mid = len(words) // 2
        word_text = ', '.join(words[:mid]) + ',\n' + ', '.join(words[mid:])
    else:
        # Split into 3 lines for Typical tier
        third = len(words) // 3
        word_text = ', '.join(words[:third]) + ',\n' + ', '.join(words[third:2*third]) + ',\n' + ', '.join(words[2*third:])

    ax.text(words_start, y, word_text,
            fontsize=9, color='white', ha='left', va='center',
            transform=ax.transAxes, linespacing=1.3)

# Add footer
ax.text(0.5, 0.02, 'TrendJournal Bot • Data from Sep 2025 - Jan 2026 • Average sentiment: 10.82%',
        fontsize=10, color='#666666', ha='center', va='bottom',
        transform=ax.transAxes)

# Add stats box
stats_text = """Historical Range:
• Daily avg: 6.1% to 19.8%
• Intraday: -4.5% to 26.6%
• Most common: ~11%"""

stats_box = mpatches.FancyBboxPatch(
    (0.78, 0.06), 0.20, 0.12,
    boxstyle="round,pad=0.02,rounding_size=0.02",
    facecolor='#2a2a4e', edgecolor='#444466',
    transform=ax.transAxes
)
ax.add_patch(stats_box)
ax.text(0.88, 0.12, stats_text, fontsize=8, color='#aaaaaa',
        ha='center', va='center', transform=ax.transAxes, linespacing=1.5)

# Save
plt.tight_layout(rect=[0, 0.03, 1, 0.95])
plt.savefig('docs/sentiment_word_mapping.png', dpi=150, facecolor='#1a1a2e',
            edgecolor='none', bbox_inches='tight', pad_inches=0.3)
plt.savefig('docs/sentiment_word_mapping_hires.png', dpi=300, facecolor='#1a1a2e',
            edgecolor='none', bbox_inches='tight', pad_inches=0.3)

print("Generated: docs/sentiment_word_mapping.png")
print("Generated: docs/sentiment_word_mapping_hires.png")
