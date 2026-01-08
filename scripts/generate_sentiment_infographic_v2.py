#!/usr/bin/env python3
"""
Generate a Bluesky-optimized PNG infographic showing the sentiment word mapping tiers.
Designed for 1200x675 (16:9) which displays well on Bluesky.
"""

import matplotlib.pyplot as plt
import matplotlib.patches as mpatches
import numpy as np

# Tier data with sample words (showing most representative)
tiers = [
    {
        "name": "Extreme Negative",
        "range": "< 0%",
        "count": 5,
        "sample": "angry • hostile • grim • miserable • dreadful",
        "color": "#EF4444",
    },
    {
        "name": "Unusually Low",
        "range": "0% – 9.5%",
        "count": 15,
        "sample": "anxious • tense • pessimistic • glum • melancholy",
        "color": "#F97316",
    },
    {
        "name": "Below Average",
        "range": "9.5% – 10.5%",
        "count": 15,
        "sample": "flat • tired • cautious • quiet • reflective",
        "color": "#EAB308",
    },
    {
        "name": "Typical",
        "range": "10.5% – 12.5%",
        "count": 30,
        "sample": "calm • content • curious • playful • sociable",
        "color": "#84CC16",
    },
    {
        "name": "Above Average",
        "range": "12.5% – 14%",
        "count": 15,
        "sample": "happy • cheerful • optimistic • friendly • bright",
        "color": "#22C55E",
    },
    {
        "name": "Unusually High",
        "range": "14% – 18%",
        "count": 15,
        "sample": "excited • vibrant • inspired • thrilled • buzzing",
        "color": "#14B8A6",
    },
    {
        "name": "Extreme Positive",
        "range": "≥ 18%",
        "count": 5,
        "sample": "euphoric • ecstatic • elated • jubilant • celebratory",
        "color": "#3B82F6",
    },
]

# Create figure - 16:9 aspect ratio optimized for Bluesky
fig, ax = plt.subplots(figsize=(12, 6.75), facecolor='#0f172a')
ax.set_facecolor('#0f172a')
ax.axis('off')

# Title
ax.text(0.5, 0.94, 'Bluesky Sentiment Word Mapping', fontsize=24, fontweight='bold',
        color='white', ha='center', va='top', transform=ax.transAxes)
ax.text(0.5, 0.87, '100 words calibrated to actual Bluesky sentiment • Based on 116 days of data',
        fontsize=11, color='#94a3b8', ha='center', va='top', transform=ax.transAxes)

# Draw tiers
y_start = 0.78
y_step = 0.105
bar_height = 0.075

for i, tier in enumerate(tiers):
    y = y_start - (i * y_step)

    # Color bar
    bar = mpatches.FancyBboxPatch(
        (0.03, y - bar_height/2), 0.015, bar_height,
        boxstyle="round,pad=0,rounding_size=0.008",
        facecolor=tier['color'], edgecolor='none',
        transform=ax.transAxes
    )
    ax.add_patch(bar)

    # Tier name
    ax.text(0.06, y + 0.012, tier['name'], fontsize=13, fontweight='bold',
            color='white', ha='left', va='center', transform=ax.transAxes)

    # Range and count
    ax.text(0.06, y - 0.018, f"{tier['range']}  •  {tier['count']} words",
            fontsize=9, color='#64748b', ha='left', va='center', transform=ax.transAxes)

    # Sample words
    ax.text(0.32, y, tier['sample'], fontsize=10, color='#cbd5e1',
            ha='left', va='center', transform=ax.transAxes)

# Stats box on right
stats_x = 0.82
ax.text(stats_x, 0.22, 'Historical Data', fontsize=11, fontweight='bold',
        color='white', ha='center', va='top', transform=ax.transAxes)

stats = [
    ('Daily avg range:', '6.1% – 19.8%'),
    ('Intraday range:', '-4.5% – 26.6%'),
    ('Overall mean:', '10.8%'),
    ('Data period:', 'Sep 2025 – Jan 2026'),
]

for j, (label, value) in enumerate(stats):
    y_stat = 0.16 - (j * 0.035)
    ax.text(stats_x - 0.08, y_stat, label, fontsize=9, color='#64748b',
            ha='left', va='center', transform=ax.transAxes)
    ax.text(stats_x + 0.08, y_stat, value, fontsize=9, color='#94a3b8',
            ha='right', va='center', transform=ax.transAxes)

# Footer
ax.text(0.5, 0.02, '@trendjournal.bsky.social',
        fontsize=10, color='#475569', ha='center', va='bottom', transform=ax.transAxes)

# Save
plt.tight_layout(pad=0.5)
plt.savefig('docs/sentiment_word_mapping_bluesky.png', dpi=150, facecolor='#0f172a',
            edgecolor='none', bbox_inches='tight', pad_inches=0.2)

print("Generated: docs/sentiment_word_mapping_bluesky.png")
