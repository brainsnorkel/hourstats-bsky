#!/bin/bash

echo "🔍 Monitoring for new Sparkline Reply functionality..."
echo "⏰ Started at: $(date)"
echo ""

# Function to check for new logs
check_logs() {
    echo "📊 Checking processor logs for TopPostURI functionality..."
    aws logs filter-log-events \
        --log-group-name "/aws/lambda/hourstats-processor" \
        --start-time $(date -v-5M +%s)000 \
        --query 'events[?contains(message, `TopPostURI`) || contains(message, `Successfully stored top post URI`)].{Time:timestamp,Message:message}' \
        --output table
    
    echo ""
    echo "📈 Checking sparkline-poster logs for reply functionality..."
    aws logs filter-log-events \
        --log-group-name "/aws/lambda/hourstats-sparkline-poster" \
        --start-time $(date -v-5M +%s)000 \
        --query 'events[?contains(message, `reply`) || contains(message, `TopPostURI`) || contains(message, `standalone`)].{Time:timestamp,Message:message}' \
        --output table
}

# Check logs every 2 minutes
while true; do
    check_logs
    echo "⏳ Waiting 2 minutes for next check..."
    sleep 120
done
