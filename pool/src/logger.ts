import type { WorkerConfig } from "./config.js";

export class Logger {
    private workerId: string;
    private logLevel: number;
    private levels: { [key: string]: number } = { 'info': 0, 'warn': 1, 'error': 2 };

    constructor(workerId: string, level: WorkerConfig['logging']['level'] = 'info') {
        this.workerId = workerId;
        this.logLevel = this.levels[level] as number;
    }

    private log(level: 'info' | 'warn' | 'error', message: string, data?: Record<string, any>): void {
        if ((this.levels[level] as number) < this.logLevel) {
            return;
        }

        const entry = {
            timestamp: new Date().toISOString(),
            level,
            message,
            workerId: this.workerId,
            ...(data && { data })
        };

        console.log(JSON.stringify(entry));
    }

    info(message: string, data?: Record<string, any>): void {
        this.log('info', message, data);
    }

    warn(message: string, data?: Record<string, any>): void {
        this.log('warn', message, data);
    }

    error(message: string, data?: Record<string, any>): void {
        this.log('error', message, data);
    }
}