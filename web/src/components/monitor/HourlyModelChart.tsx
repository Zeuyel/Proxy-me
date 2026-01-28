import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from 'recharts';
import type { UsageData } from '@/pages/MonitorPage';
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/Card';
import {
  ChartConfig,
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from '@/components/ui/chart';
import styles from '@/pages/MonitorPage.module.scss';

interface HourlyModelChartProps {
  data: UsageData | null;
  loading: boolean;
  isDark: boolean;
}

type HourRange = 6 | 12 | 24;

const CHART_COLORS = [
  'var(--chart-1)',
  'var(--chart-2)',
  'var(--chart-3)',
  'var(--chart-4)',
  'var(--chart-5)',
  '#06b6d4',
  '#eab308',
  '#ef4444',
  '#14b8a6',
  '#6366f1',
];

const MAX_MODELS = 6;

export function HourlyModelChart({ data, loading }: HourlyModelChartProps) {
  const { t } = useTranslation();
  const [hourRange, setHourRange] = useState<HourRange>(12);

  const { chartData, chartConfig, modelSeries, hasData } = useMemo<{
    chartData: Array<Record<string, number | string>>;
    chartConfig: ChartConfig;
    modelSeries: Array<{ key: string; modelName: string }>;
    hasData: boolean;
  }>(() => {
    if (!data?.apis) {
      return { chartData: [], chartConfig: {}, modelSeries: [], hasData: false };
    }

    const now = new Date();
    const cutoffTime = new Date(now.getTime() - hourRange * 60 * 60 * 1000);
    cutoffTime.setMinutes(0, 0, 0);
    const hoursCount = hourRange + 1;

    const allHours: string[] = [];
    for (let i = 0; i < hoursCount; i++) {
      const hourTime = new Date(cutoffTime.getTime() + i * 60 * 60 * 1000);
      const hourKey = hourTime.toISOString().slice(0, 13);
      allHours.push(hourKey);
    }

    const hourlyStats: Record<string, Record<string, number>> = {};
    allHours.forEach((hour) => {
      hourlyStats[hour] = {};
    });

    const modelTotals: Record<string, number> = {};
    let totalRequests = 0;

    Object.values(data.apis).forEach((apiData) => {
      Object.entries(apiData.models).forEach(([modelName, modelData]) => {
        modelData.details.forEach((detail) => {
          const timestamp = new Date(detail.timestamp);
          if (timestamp < cutoffTime) return;

          const hourKey = timestamp.toISOString().slice(0, 13);
          if (!hourlyStats[hourKey]) return;

          hourlyStats[hourKey][modelName] = (hourlyStats[hourKey][modelName] || 0) + 1;
          modelTotals[modelName] = (modelTotals[modelName] || 0) + 1;
          totalRequests += 1;
        });
      });
    });

    const topModels = Object.entries(modelTotals)
      .sort((a, b) => b[1] - a[1])
      .slice(0, MAX_MODELS)
      .map(([name]) => name);

    const modelSeries = topModels.map((modelName, index) => ({
      key: `model${index}`,
      modelName,
    }));

    const chartData = allHours.sort().map((hour) => {
      const date = new Date(`${hour}:00:00Z`);
      const row: Record<string, number | string> = {
        hour: `${date.getHours()}:00`,
      };
      modelSeries.forEach(({ key, modelName }) => {
        row[key] = hourlyStats[hour]?.[modelName] || 0;
      });
      return row;
    });

    const chartConfig: ChartConfig = {};
    modelSeries.forEach(({ key, modelName }, index) => {
      chartConfig[key] = {
        label: modelName,
        color: CHART_COLORS[index % CHART_COLORS.length],
      };
    });

    return { chartData, chartConfig, modelSeries, hasData: totalRequests > 0 };
  }, [data, hourRange]);

  const hourRangeLabel = useMemo(() => {
    if (hourRange === 6) return t('monitor.hourly.last_6h');
    if (hourRange === 12) return t('monitor.hourly.last_12h');
    return t('monitor.hourly.last_24h');
  }, [hourRange, t]);

  return (
    <Card className={styles.chartCard}>
      <CardHeader className={styles.chartHeader}>
        <div>
          <CardTitle className={styles.chartTitle}>{t('monitor.hourly_model.title')}</CardTitle>
          <CardDescription className={styles.chartSubtitle}>
            {hourRangeLabel}
          </CardDescription>
        </div>
        <div className={styles.chartControls}>
          <button
            className={`${styles.chartControlBtn} ${hourRange === 6 ? styles.active : ''}`}
            onClick={() => setHourRange(6)}
          >
            {t('monitor.hourly.last_6h')}
          </button>
          <button
            className={`${styles.chartControlBtn} ${hourRange === 12 ? styles.active : ''}`}
            onClick={() => setHourRange(12)}
          >
            {t('monitor.hourly.last_12h')}
          </button>
          <button
            className={`${styles.chartControlBtn} ${hourRange === 24 ? styles.active : ''}`}
            onClick={() => setHourRange(24)}
          >
            {t('monitor.hourly.last_24h')}
          </button>
        </div>
      </CardHeader>

      <CardContent className={styles.chartContent}>
        {loading || !hasData ? (
          <div className={styles.chartEmpty}>
            {loading ? t('common.loading') : t('monitor.no_data')}
          </div>
        ) : (
          <ChartContainer
            config={chartConfig}
            className={`${styles.lineChartContainer} aspect-auto h-[250px] w-full`}
          >
            <BarChart
              accessibilityLayer
              data={chartData}
              margin={{
                top: 20,
                left: 12,
                right: 12,
              }}
            >
              <CartesianGrid vertical={false} />
              <XAxis
                dataKey="hour"
                tickLine={false}
                axisLine={false}
                tickMargin={8}
              />
              <YAxis
                tickLine={false}
                axisLine={false}
                tickMargin={8}
              />
              <ChartTooltip
                cursor={{ fill: "#8080804D" }}
                content={<ChartTooltipContent />}
              />
              {modelSeries.map(({ key }, index) => (
                <Bar
                  key={key}
                  dataKey={key}
                  stackId="requests"
                  fill={`var(--color-${key})`}
                  radius={index === modelSeries.length - 1 ? [4, 4, 0, 0] : [0, 0, 0, 0]}
                />
              ))}
            </BarChart>
          </ChartContainer>
        )}
      </CardContent>
    </Card>
  );
}
