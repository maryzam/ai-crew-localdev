package telemetry

func usageString(usage *Usage, pick func(*Usage) string) any {
	if usage == nil {
		return nil
	}
	return pick(usage)
}

func usageCostAmount(usage *Usage) any {
	if usage == nil || usage.CostAmount == nil {
		return nil
	}
	return *usage.CostAmount
}

func usageCostCurrency(usage *Usage) any {
	if usage == nil {
		return nil
	}
	return usage.CostCurrency
}
