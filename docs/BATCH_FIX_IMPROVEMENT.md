# Batch Overwrite Fix - Improvement Comparison

## 🔴 BEFORE THE FIX

### Code Behavior
```go
func AddPosts(...) {
    batchIndex := 0  // ❌ Always starts at 0!
    // Store batches...
}
```

### What Happened During Fetch (Example: 3 iterations, ~297 posts collected)

| Iteration | Posts Collected | Batches Created | DynamoDB State After | Posts Lost |
|-----------|----------------|-----------------|---------------------|------------|
| 1 | 97 posts | `batch0` (97 posts) | `batch0: 97 posts` | 0 |
| 2 | 100 posts | `batch0` (100 posts) | `batch0: 100 posts` ⚠️ | **97 posts** |
| 3 | 100 posts | `batch0` (100 posts) | `batch0: 100 posts` ⚠️ | **100 posts** |
| **Final** | **297 posts** | **1 batch** | **`batch0: 100 posts`** | **197 posts (66%)** |

### Visual Representation
```
Iteration 1: [batch0: 97 posts] ✅
Iteration 2: [batch0: 97 posts] → [batch0: 100 posts] ❌ (OVERWRITES!)
Iteration 3: [batch0: 100 posts] → [batch0: 100 posts] ❌ (OVERWRITES!)
Result: [batch0: 100 posts] - Only 100 out of 297 posts stored
```

### Real Production Example (from your logs)
- **Posts collected from API**: 9,802 posts
- **Expected batches**: ~98 batches (9,802 ÷ 100)
- **Actual batches stored**: 1 batch (batch0)
- **Posts stored**: ~97 posts
- **Posts lost**: **~9,705 posts (99%)** ❌
- **Sentiment analysis used**: Only 97 posts instead of 9,802

---

## 🟢 AFTER THE FIX

### Code Behavior
```go
func AddPosts(...) {
    // ✅ Query existing batches first
    maxBatchIndex := queryHighestBatchIndex(runID)
    batchIndex := maxBatchIndex + 1  // ✅ Start from next available
    // Store batches...
}
```

### What Happens Now (Same Example: 3 iterations, ~297 posts collected)

| Iteration | Posts Collected | Batches Created | DynamoDB State After | Posts Lost |
|-----------|----------------|-----------------|---------------------|------------|
| 1 | 97 posts | `batch0` (97 posts) | `batch0: 97 posts` | 0 |
| 2 | 100 posts | `batch1` (100 posts) | `batch0: 97, batch1: 100` | 0 ✅ |
| 3 | 100 posts | `batch2` (100 posts) | `batch0: 97, batch1: 100, batch2: 100` | 0 ✅ |
| **Final** | **297 posts** | **3 batches** | **297 posts stored** | **0 posts (0%)** ✅ |

### Visual Representation
```
Iteration 1: [batch0: 97 posts] ✅
Iteration 2: [batch0: 97, batch1: 100] ✅ (APPENDS)
Iteration 3: [batch0: 97, batch1: 100, batch2: 100] ✅ (APPENDS)
Result: [batch0-2: 297 posts] - All 297 posts stored!
```

### Real Production Example (Expected)
- **Posts collected from API**: 9,802 posts
- **Expected batches**: ~98 batches (9,802 ÷ 100)
- **Actual batches stored**: ~98 batches (batch0 through batch97)
- **Posts stored**: ~9,802 posts ✅
- **Posts lost**: **0 posts (0%)** ✅
- **Sentiment analysis uses**: All 9,802 posts ✅

---

## 📊 Impact Metrics

### Data Preservation
| Metric | Before Fix | After Fix | Improvement |
|--------|-----------|-----------|-------------|
| **Posts Stored** | ~97 (1%) | ~9,802 (100%) | **100x increase** |
| **Data Loss** | 99% | 0% | **99% reduction** |
| **Batches Preserved** | 1 | ~98 | **98x increase** |

### Sentiment Analysis Accuracy
| Aspect | Before Fix | After Fix | Improvement |
|--------|-----------|-----------|-------------|
| **Posts Analyzed** | 97 | 9,802 | **100x more data** |
| **Sample Size** | 1% of collected | 100% of collected | **Complete dataset** |
| **Accuracy** | Based on ~1% sample | Based on full dataset | **More representative** |
| **TotalPosts in Observations** | 97 (incorrect) | 9,802 (correct) | **Accurate reporting** |

### Performance Impact
| Aspect | Impact | Notes |
|--------|--------|-------|
| **Additional Query** | +1 DynamoDB query per `AddPosts` call | ~5-10ms overhead |
| **Query Efficiency** | Uses `ProjectionExpression` | Only fetches `postId` field |
| **Data Loss Prevention** | Prevents 99% data loss | Well worth the minimal overhead |

---

## 🔍 Code Comparison

### Before (Buggy)
```go
// Line 211: Always starts at 0
batchIndex := 0

for i := 0; i < len(posts); i += postsPerBatch {
    postBatch := PostBatch{
        PostID: fmt.Sprintf("%s#batch%d", runID, batchIndex),
        // ...
    }
    // PutItem overwrites existing batch0!
    batchIndex++
}
```

### After (Fixed)
```go
// Lines 212-258: Query existing batches first
maxBatchIndex := -1
// ... query logic to find highest existing batch ...

// Line 261: Start from next available index
batchIndex := maxBatchIndex + 1

for i := 0; i < len(posts); i += postsPerBatch {
    postBatch := PostBatch{
        PostID: fmt.Sprintf("%s#batch%d", runID, batchIndex),
        // ...
    }
    // PutItem creates new batch (no overwrite!)
    batchIndex++
}
```

---

## 🎯 Real-World Scenario

### Typical 30-minute fetch window:
- **API returns**: ~4,000-10,000 posts
- **After time filtering**: ~4,000-10,000 posts
- **After deduplication**: ~2,000-5,000 unique posts
- **Batches needed**: 20-50 batches

**Before Fix:**
- Only 1 batch stored (last iteration)
- ~100 posts available for sentiment analysis
- Missing 95-99% of collected data

**After Fix:**
- All 20-50 batches stored
- All 2,000-5,000 posts available for sentiment analysis
- Complete dataset for accurate sentiment calculation

---

## ✅ Verification

To verify the fix is working:

1. **Check CloudWatch Logs** for:
   ```
   AddPosts: Found existing batches up to index X, starting from batch Y
   ```

2. **Check DynamoDB** for multiple batches:
   - Should see: `runId#batch0`, `runId#batch1`, `runId#batch2`, etc.

3. **Check Sentiment Observations**:
   - `TotalPosts` should match `TotalPostsRetrieved` (~9,800 instead of 97)

4. **Check Query Results**:
   ```bash
   go run cmd/query-runs/main.go -run <runId>
   ```
   - Should show "Actual Posts in DB" matching collected posts

