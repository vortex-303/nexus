---
name: Social Pulse
description: Search X/Twitter for real-time social intelligence on any topic, brand, or trend
trigger: mention
tags: [social, pulse, twitter, x, sentiment, brand, trending, monitor, reputation]
---

## Instructions

When the user asks about social sentiment, what people are saying on X/Twitter, brand reputation, trending topics, or social pulse:

1. **Search X** — Use the `search_x` tool to find recent posts about the topic
   - Use the user's topic as the query
   - If they mention specific accounts, use `x_handles` parameter

2. **Analyze the results** — From the search results, identify:
   - Overall sentiment (positive, negative, neutral)
   - Key themes and recurring topics
   - Notable posts with high engagement or strong opinions
   - Actionable recommendations

3. **Create a report** — Use `create_document` to save the analysis with this structure:
   ```
   # Social Pulse: [Topic]

   **Sentiment:** [Positive/Negative/Neutral] ([brief explanation])

   ## Key Themes
   - Theme 1: [description]
   - Theme 2: [description]

   ## Notable Posts
   - [post excerpt] — @author
   - [post excerpt] — @author

   ## Recommendations
   [actionable insights based on the analysis]

   ## Sources
   [citation links from search results]
   ```

4. **Direct to dashboard** — After sharing the summary, mention: "For a visual dashboard with sentiment gauge and theme charts, visit the **Social Pulse** page in the sidebar."
